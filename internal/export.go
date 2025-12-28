package internal

import (
	"encoding/csv"
	"fmt"
	"log"
	"mqtg-bot/internal/models"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (bot *TelegramBot) ExportSubscriptionToCSV(chatID int64, subID uint) {
	var data []models.SubscriptionData
	// Recupera i dati
	result := bot.db.Where("subscription_id = ?", subID).Order("date_time asc").Find(&data)
	if result.Error != nil {
		log.Printf("Errore query export: %v", result.Error)
		return
	}

	fileName := fmt.Sprintf("export_%d.csv", subID)

	file, err := os.Create(fileName)
	if err != nil {
		log.Printf("Errore creazione file: %v", err)
		return
	}

	writer := csv.NewWriter(file)
	writer.Write([]string{"Data", "ID", "Messaggio"})

	for _, row := range data {
		writer.Write([]string{
			row.DateTime.Format("2006-01-02 15:04:05"),
			fmt.Sprintf("%d", row.SubscriptionID),
			string(row.Data),
		})
	}
	writer.Flush()
	file.Close()

	// --- LOGICA DI INVIO COMPATIBILE V5 ---
	// Usiamo NewDocument (o NewDocumentUpload) con FileBytes
	fileBytes, err := os.ReadFile(fileName)
	if err != nil {
		log.Printf("Errore lettura file: %v", err)
		return
	}

	fb := tgbotapi.FileBytes{
		Name:  fileName,
		Bytes: fileBytes,
	}

	// Proviamo NewDocument (se fallisce, il compilatore suggerirà il nome corretto)
	// In alcune versioni v5 è NewDocument, in altre NewDocumentUpload
	//msg := tgbotapi.NewDocument(chatID, fb)
	msg := tgbotapi.NewDocumentUpload(chatID, fb)
	msg.Caption = fmt.Sprintf("Storico sottoscrizione #%d", subID)

	_, err = bot.Request(msg)
	if err != nil {
		log.Printf("Errore invio Telegram: %v", err)
	}

	// Rimuovi il file temporaneo
	os.Remove(fileName)
}
