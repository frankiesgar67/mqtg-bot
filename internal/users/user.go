package users

import (
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	paho "github.com/eclipse/paho.mqtt.golang" // Alias paho per risolvere il tipo undefined
	"gorm.io/gorm"
	"log"
	"mqtg-bot/internal/common"
	"mqtg-bot/internal/models"
	"mqtg-bot/internal/users/keyboard"
	"mqtg-bot/internal/users/menu"
	"mqtg-bot/internal/users/mqtt"
	"mqtg-bot/internal/users/state"
	"sync"
	"time"
)

type User struct {
	*models.DbUser
	sync.Mutex
	db *gorm.DB

	mqtt           *mqtt.Client
	subscriptionCh chan mqtt.SubscriptionMessage

	menu  *menu.MainMenu
	state state.StateStruct

	lastCallbackData string
	lastCallbackTime time.Time
}

func (user *User) Start() *common.BotMessage {
	msg := "Welcome to MQTT Client Telegram Bot"
	if !user.isMqttConnected() {
		msg += "\nConfigure connection to your MQTT broker"
	}
	user.menu.ResetCurrentPath()
	user.state.Reset()

	return &common.BotMessage{
		MainText: msg,
		MainMenu: user.getMainMenu(),
	}
}

func (user *User) ConfigureConnection() *common.BotMessage {
	user.menu.ResetCurrentPath()
	if user.isMqttConnected() {
		return &common.BotMessage{
			MainText: "You are already connected to MQTT, need to disconnect first",
			MainMenu: user.getMainMenu(),
		}
	}

	inlineText, inlineKeyboard := keyboard.GetConnectionStringKeyboard(user.MqttUrl)

	return &common.BotMessage{
		InlineText:     inlineText,
		InlineKeyboard: inlineKeyboard,
	}
}

func (user *User) Back() []byte {
	user.state.Reset()
	user.menu.Back()
	messageData := []byte(user.menu.CurrPath.GetName())
	user.menu.Back()   // due volte indietro per la navigazione corretta
	return messageData // e poi avanti
}

func (user *User) subscribe(newSubscription *models.Subscription) int32 {
	user.db.Create(newSubscription)
	user.mqtt.Subscribe(newSubscription)
	user.Subscriptions = append(user.Subscriptions, newSubscription)
	log.Printf("User %v (Chat.ID %v) subscribed to the topic %v", user.UserName, user.ChatID, newSubscription.Topic)

	return int32(len(user.Subscriptions) - 1)
}

func (user *User) unsubscribe(subscription *models.Subscription, subscriptionIndex int32) {
	user.mqtt.Unsubscribe(subscription)
	user.db.Delete(subscription)
	user.Subscriptions = append(user.Subscriptions[:subscriptionIndex], user.Subscriptions[subscriptionIndex+1:]...)
	log.Printf("User %v (Chat.ID %v) unsubscribed from the topic %v", user.UserName, user.ChatID, subscription.Topic)
}

func (user *User) publish(payload interface{}) {
	topic := user.state.PublishingTopic
	user.mqtt.Publish(topic, user.state.Qos, user.state.Retained, payload)
	log.Printf("User %v (Chat.ID %v) published to the topic %v", user.UserName, user.ChatID, topic)
}

func (user *User) setConnected(value bool) {
	user.Connected = value
	user.db.Model(&user.DbUser).Update("connected", value)
}

func (user *User) saveMqttUrl() {
	user.db.Model(&user.DbUser).Update("mqtt_url", user.MqttUrl)
}

func (user *User) isMqttConnected() bool {
	return user.Connected && user.mqtt != nil && user.mqtt.IsConnected()
}

func (user *User) disconnectMQTT() {
	if user.mqtt != nil {
		user.mqtt.Disconnect(100) // ms wait timeout
	}
	if user.Connected {
		user.setConnected(false)
	}
}

func (user *User) getMainMenu() *tgbotapi.ReplyKeyboardMarkup {
	if !user.isMqttConnected() {
		return &menu.ConfigureConnectionMenu
	}
	return user.menu.GetCurrPathMainMenu()
}

// connectMqttAndSubscribe gestisce la connessione e imposta il ripristino automatico delle sottoscrizioni
func (user *User) connectMqttAndSubscribe() error {
	// Definiamo cosa fare quando il client si riconnette al broker
	onConnect := func(c paho.Client) { 
		log.Printf("🔄 MQTT Reconnected for user %v. Restoring %d subscriptions...", user.UserName, len(user.Subscriptions))
		
		user.Lock()
		// Copia delle sottoscrizioni per evitare problemi di concorrenza (Race Conditions)
		subs := make([]*models.Subscription, len(user.Subscriptions))
		copy(subs, user.Subscriptions)
		user.Unlock()

		for _, subscription := range subs {
			subscription.UserMutex = &user.Mutex
			if user.mqtt != nil {
				user.mqtt.Subscribe(subscription)
				log.Printf("📡 Restored subscription to: %s", subscription.Topic)
			}
		}
	}

	var err error
	// Passiamo onConnect alla nostra funzione Connect locale nel pacchetto mqtt
	user.mqtt, err = mqtt.Connect(user.DbUser, user.subscriptionCh, onConnect)
	if err != nil {
		return err
	}

	user.setConnected(true)
	user.saveMqttUrl()
	log.Printf("✅ Connect user %v (Chat.ID %v) to mqtt", user.UserName, user.ChatID)
	
	return nil
}

func (user *User) SaveMenuIntoDB() {
	jsonMenu, err := user.menu.Marshal()
	if err != nil {
		log.Printf("Menu marshal error: %v", err)
		return
	}
	user.DbMenu = jsonMenu
	user.db.Save(&user.DbUser)
}

func (user *User) processConnectionString(mqttUrl string) *common.BotMessage {
	user.disconnectMQTT() // Sincronizza lo stato disconnettendo sessioni orfane

	if len(mqttUrl) > 0 { 
		user.MqttUrl = mqttUrl
	}
	err := user.connectMqttAndSubscribe()

	var userAnswer common.BotMessage
	if err != nil {
		userAnswer.MainText = fmt.Sprintf("Could not connect to MQTT url: %v. Error: %v", user.MqttUrl, err)
	} else {
		userAnswer.MainText = "Successfully connected to MQTT broker"
	}
	userAnswer.MainMenu = user.getMainMenu()

	return &userAnswer
}
