package mqtt

import (
	"fmt"
	paho "github.com/eclipse/paho.mqtt.golang" // Alias paho per evitare conflitti
	"mqtg-bot/internal/models"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Client struct {
	paho.Client
	subscriptionCh chan SubscriptionMessage
}

type SubscriptionMessage struct {
	Message      paho.Message
	Subscription *models.Subscription
}

// Connect inizializza la connessione configurando KeepAlive, CleanSession e la callback di riconnessione
func Connect(dbUser *models.DbUser, subscriptionCh chan SubscriptionMessage, onConnectCallback func(paho.Client)) (*Client, error) {
	uri, err := url.Parse(dbUser.MqttUrl)
	if err != nil {
		return nil, fmt.Errorf("could not parse MqttUrl: %v", err)
	}

	clientOptions := paho.NewClientOptions()
	
	// --- CONFIGURAZIONE RESILIENZA E RICONNESSIONE ---
	clientOptions.SetAutoReconnect(true)
	clientOptions.SetMaxReconnectInterval(1 * time.Minute)
	clientOptions.SetOnConnectHandler(onConnectCallback) 

	// Gestione MQTT_CLEAN_SESSION (Default: false per mantenere la sessione sul broker)
	// SE VUOI CANCELLARE LE CODE ALL'AVVIO:
	// Imposta la variabile d'ambiente (file .ENV) MQTT_CLEAN_SESSION=true
	// Questo farà sì che il broker dimentichi i messaggi vecchi ogni volta che il bot parte.
	cleanSession := false
	if os.Getenv("MQTT_CLEAN_SESSION") == "true" {
		cleanSession = true
	}
	clientOptions.SetCleanSession(cleanSession)

	// Gestione MQTT_KEEPALIVE (Default: 60 secondi)
	keepAlive := int64(60)
	if kaStr := os.Getenv("MQTT_KEEPALIVE"); kaStr != "" {
		if val, err := strconv.ParseInt(kaStr, 10, 64); err == nil {
			keepAlive = val
		}
	}
	clientOptions.SetKeepAlive(time.Duration(keepAlive) * time.Second)
	// ------------------------------------------------

	clientOptions.AddBroker(fmt.Sprintf("%s://%s", uri.Scheme, uri.Host))
	clientOptions.SetUsername(uri.User.Username())
	password, _ := uri.User.Password()
	clientOptions.SetPassword(password)
	
	clientId := os.Getenv("MQTT_CLIENT_ID")
	if clientId != "" {
		clientOptions.SetClientID(clientId)
	} else {
		clientOptions.SetClientID(fmt.Sprintf("mqtg-%d", time.Now().Unix()))
	}

	client := paho.NewClient(clientOptions)

	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return nil, fmt.Errorf("MQTT connection timeout")
	}

	if err := token.Error(); err != nil {
		return nil, err
	}

	return &Client{
		Client:         client,
		subscriptionCh: subscriptionCh,
	}, nil
}

func (c *Client) Subscribe(subscription *models.Subscription) {
	c.Client.Subscribe(subscription.Topic, subscription.Qos, func(client paho.Client, msg paho.Message) {
		c.subscriptionCh <- SubscriptionMessage{
			Message:      msg,
			Subscription: subscription,
		}
	})
}

func (c *Client) Unsubscribe(subscription *models.Subscription) {
	c.Client.Unsubscribe(subscription.Topic)
}

func (c *Client) Publish(topic string, qos byte, retained bool, payload interface{}) {
	c.Client.Publish(topic, qos, retained, payload)
}
