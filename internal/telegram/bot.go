package telegram

import (
	"log"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

func RunBot() {
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️ .env fayl topilmadi, davom etamiz...")
	}

	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		log.Fatal("❌ TELEGRAM_BOT_TOKEN yo‘q, .env faylni tekshir!")
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Fatal("❌ Bot yaratishda xatolik:", err)
	}

	bot.Debug = true
	log.Printf("🤖 Bot ishga tushdi: %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil { 
			continue
		}
		HandleMessage(bot, update.Message)
	}
}
