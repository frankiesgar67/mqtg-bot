package internal

import (
	"encoding/json"
	"fmt"
	"log"
	"mqtg-bot/internal/common"
	"mqtg-bot/internal/models"
	"mqtg-bot/internal/users/menu/button_names"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/oliveagle/jsonpath"
)

func (bot *TelegramBot) StartBotListener() {
	defer bot.wg.Done()

	log.Printf("Start Telegram Bot Listener")

	// Recupero della configurazione per l'età massima dei messaggi dal .env
	maxAge := int64(30) // Default 30 secondi
	if val := os.Getenv("TELEGRAM_MAX_MESSAGE_AGE"); val != "" {
		if parsedAge, err := strconv.ParseInt(val, 10, 64); err == nil {
			maxAge = parsedAge
			log.Printf("Configurato filtro età messaggi Telegram: %d secondi", maxAge)
		}
	}

	for {
		select {
		case <-bot.shutdownChannel:
			log.Printf("Telegram Bot Listener received shutdown signal")
			return

		case subscriptionMessage := <-bot.subscriptionCh:
			// --- GESTIONE MESSAGGI MQTT IN ARRIVO ---

			// FILTRO ANTI-COMANDI VECCHI (RETAINED)
			if subscriptionMessage.Message.Retained() {
				log.Printf("🗑️ Ignorato messaggio MQTT Retained (vecchio) sul topic: %s", subscriptionMessage.Message.Topic())
				continue
			}

			subscriptionMessage.Subscription.UserMutex.Lock()

			var formattedMessage string
			beforeValueText := strings.ReplaceAll(subscriptionMessage.Subscription.BeforeValueText, "%s", "<code>"+subscriptionMessage.Subscription.Topic+"</code>")
			beforeValueText = strings.ReplaceAll(beforeValueText, "%t", "<code>"+subscriptionMessage.Message.Topic()+"</code>")

			if subscriptionMessage.Subscription.DataType == models.IMAGE_DATA_TYPE {
				formattedMessage = beforeValueText
				subscriptionMessage.Subscription.LastValuePayload = subscriptionMessage.Message.Payload()
			} else {
				afterValueText := strings.ReplaceAll(subscriptionMessage.Subscription.AfterValueText, "%s", "<code>"+subscriptionMessage.Subscription.Topic+"</code>")
				afterValueText = strings.ReplaceAll(afterValueText, "%t", "<code>"+subscriptionMessage.Message.Topic()+"</code>")

				payload := subscriptionMessage.Message.Payload()
				if len(subscriptionMessage.Subscription.JsonPathToData) > 1 {
					var jsonData interface{}
					err := json.Unmarshal(payload, &jsonData)
					if err == nil {
						result, err := jsonpath.JsonPathLookup(jsonData, subscriptionMessage.Subscription.JsonPathToData)
						if err == nil {
							payload = []byte(fmt.Sprintf("%v", result))
						}
					}
				}
				formattedMessage = fmt.Sprintf("%v %v %v", beforeValueText, string(payload), afterValueText)
				subscriptionMessage.Subscription.LastValuePayload = payload
			}

			subscriptionMessage.Subscription.LastValueFormattedMessage = formattedMessage
			bot.db.Save(subscriptionMessage.Subscription)

			// Salvataggio nel database se previsto
			switch subscriptionMessage.Subscription.SubscriptionType {
			case models.PRINT_AND_STORE_MESSAGE_SUBSCRIPTION_TYPE, models.SILENT_STORE_MESSAGE_SUBSCRIPTION_TYPE:
				newData := models.SubscriptionData{
					SubscriptionID: subscriptionMessage.Subscription.ID,
					DateTime:       time.Now(),
					DataType:       subscriptionMessage.Subscription.DataType,
					Data:           subscriptionMessage.Subscription.LastValuePayload,
				}
				bot.db.Create(&newData)
				if bot.maxSubDataCount > 0 && bot.maxSubDataCount < newData.ID {
					bot.db.Unscoped().Delete(models.SubscriptionData{}, "id <= ?", newData.ID-bot.maxSubDataCount)
				}
			}

			// Invio notifica Telegram
			switch subscriptionMessage.Subscription.SubscriptionType {
			case models.PRINT_AND_STORE_MESSAGE_SUBSCRIPTION_TYPE, models.PRINT_MESSAGE_WITHOUT_STORING_SUBSCRIPTION_TYPE:
				if subscriptionMessage.Subscription.DataType == models.IMAGE_DATA_TYPE {
					bot.NewPhotoUpload(subscriptionMessage.Subscription.ChatID, subscriptionMessage.Subscription.LastValueFormattedMessage, subscriptionMessage.Subscription.LastValuePayload, nil)
				} else {
					bot.SendMessage(subscriptionMessage.Subscription.ChatID, subscriptionMessage.Subscription.LastValueFormattedMessage, nil)
				}
			}
			subscriptionMessage.Subscription.UserMutex.Unlock()

		case update := <-bot.updates:
			// --- GESTIONE AGGIORNAMENTI DA TELEGRAM ---

			// FILTRO ANTI-COMANDI TELEGRAM ACCUMULATI OFFLINE
			var msgTime int
			if update.Message != nil {
				msgTime = update.Message.Date
			} else if update.CallbackQuery != nil && update.CallbackQuery.Message != nil {
				msgTime = update.CallbackQuery.Message.Date
			}

			if msgTime > 0 && int64(msgTime) < (time.Now().Unix()-maxAge) {
				log.Printf("🗑️ Scartato update Telegram obsoleto (inviato il %v)", time.Unix(int64(msgTime), 0))
				continue
			}

			bot.metrics.numOfIncMessagesFromTelegram.Inc()

			var telegramUserID int64
			var currentChatID int64

			if update.Message != nil {
				telegramUserID = int64(update.Message.From.ID)
				currentChatID = update.Message.Chat.ID
			} else if update.CallbackQuery != nil {
				telegramUserID = int64(update.CallbackQuery.From.ID)
				currentChatID = update.CallbackQuery.Message.Chat.ID
			}

			// Controllo ACL
			if len(bot.authorizedUsers) > 0 && !bot.authorizedUsers[telegramUserID] {
				log.Printf("🚫 ACL: Accesso negato per ID %d", telegramUserID)
				msg := tgbotapi.NewMessage(currentChatID, "⛔ Accesso negato.")
				bot.Send(msg)
				continue
			}

			user := bot.usersManager.GetUserByChatIdFromUpdate(&update)
			if user == nil {
				continue
			}
			user.Lock()

			var message = update.Message
			var userAnswer *common.BotMessage

			// CASO 1: MESSAGGIO DI TESTO
			if message != nil {
				// COMANDO STATUS: Verifica se il server Debian e il Bot sono online
				if message.Text == "/status" {
					telegramTime := time.Unix(int64(message.Date), 0).Format("02-01-2006 15:04:05")
					statusMsg := fmt.Sprintf("✅ <b>Sistema Online</b>\n\n"+
						"🖥️ <b>Server:</b> Debian\n"+
						"🕒 <b>Ora Server:</b> %s\n"+
						"🌐 <b>Ora Telegram:</b> %s\n"+
						"📡 <b>Filtro Messaggi:</b> %ds",
						time.Now().Format("02-01-2006 15:04:05"),
						telegramTime,
						maxAge)
					bot.SendMessage(currentChatID, statusMsg, nil)
					user.Unlock()
					continue
				}

				if strings.HasPrefix(message.Text, "/export_") {
					var id uint
					fmt.Sscanf(message.Text, "/export_%d", &id)
					if id > 0 {
						go bot.ExportSubscriptionToCSV(currentChatID, id)
						user.Unlock()
						continue
					}
				}

				messageData := []byte(message.Text)
				var isItPhoto bool
				if message.Photo != nil {
					photoData, err := bot.DownloadPhoto(message.Photo)
					if err == nil {
						messageData = photoData
						isItPhoto = true
					}
				}

				switch message.Text {
				case button_names.START:
					userAnswer = user.Start()
				case button_names.CONFIGURE_CONNECTION:
					userAnswer = user.ConfigureConnection()
				case button_names.BACK:
					messageData = user.Back()
					fallthrough
				default:
					userAnswer = user.ProcessMessage(messageData, isItPhoto)
				}

			// CASO 2: CLICK SU PULSANTE INLINE (CALLBACK)
			} else if update.CallbackQuery != nil {
				callbackData := update.CallbackQuery.Data

				if strings.HasPrefix(callbackData, "export_") {
					var subID uint
					fmt.Sscanf(callbackData, "export_%d", &subID)
					if subID > 0 {
						bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "Generazione CSV..."))
						go bot.ExportSubscriptionToCSV(currentChatID, subID)
						user.Unlock()
						continue
					}
				}

				// Processa il click
				userAnswer = user.ProcessCallback(update.CallbackQuery)

				// PROTEZIONE STATO PERSO
				if userAnswer == nil {
					log.Printf("⚠️ Stato perso per utente %d. Forzo reset menu.", telegramUserID)
					userAnswer = user.Start()
				}
			}

			user.Unlock()

			if userAnswer != nil {
				bot.SendAnswer(currentChatID, userAnswer)
			}
		}
	}
}

func (bot *TelegramBot) getUserName(update tgbotapi.Update) string {
	var from *tgbotapi.User
	if update.Message != nil {
		from = update.Message.From
	
