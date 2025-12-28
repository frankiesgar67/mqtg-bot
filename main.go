package main

import (
	"github.com/joho/godotenv"
	"log"
	"mqtg-bot/internal"
	"os"
	"os/signal"
	"strconv"   // Necessario per convertire ID da stringa a int64
	"strings"   // Necessario per separare gli ID con la virgola
	"syscall"
)

// Funzione per ottenere gli ID autorizzati dal .env
// Inserita da F. Sgarella il 25-12-2025
func getAuthorizedUsers() map[int64]bool {
	usersMap := make(map[int64]bool)
	rawUsers := os.Getenv("AUTHORIZED_USERS")

	if rawUsers == "" {
		log.Println("⚠️ ATTENZIONE: Nessun utente autorizzato configurato in AUTHORIZED_USERS")
		return usersMap
	}

	ids := strings.Split(rawUsers, ",")
	for _, idStr := range ids {
		trimmedID := strings.TrimSpace(idStr)
		if trimmedID == "" {
			continue
		}
		id, err := strconv.ParseInt(trimmedID, 10, 64)
		if err != nil {
			log.Printf("❌ Errore nel parsing dell'ID utente '%s': %v", idStr, err)
			continue
		}
		usersMap[id] = true
	}
	return usersMap
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Printf("Using environment variables from container environment")
	} else {
		log.Printf("Using environment variables from .env file")
	}

	// Recupero la mappa ACL
	authUsers := getAuthorizedUsers()

	// Inizializzo il bot passando la mappa ACL
	bot := internal.InitTelegramBot(authUsers)

	var gracefulStop = make(chan os.Signal)
	signal.Notify(gracefulStop, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-gracefulStop
		log.Printf("Caught system sig: %+v", sig)
		bot.Shutdown()
	}()

	bot.Wait()
}
