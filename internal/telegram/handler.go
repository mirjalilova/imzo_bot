package telegram

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func HandleMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	switch msg.Text {
	case "/start":
		startMsg := "Salom 👋\nMen Imzo AI bilan ishlaydigan botman.\n" +
			"Menga PDF ID yuborsang, men senga link qaytaraman."
		reply(bot, msg.Chat.ID, startMsg)

	default:
		handlePdfRequest(bot, msg)
	}
}

func handlePdfRequest(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	apiURL := os.Getenv("IMZO_AI_API_URL")
	if apiURL == "" {
		reply(bot, msg.Chat.ID, "❌ API manzili sozlanmagan!")
		return
	}

	// foydalanuvchi yuborgan ID asosida so‘rov yuborish
	url := fmt.Sprintf("%s?pdf_category_item_id=%s", apiURL, msg.Text)

	resp, err := http.Get(url)
	if err != nil {
		reply(bot, msg.Chat.ID, "❌ API ga ulanishda xatolik!")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		reply(bot, msg.Chat.ID, "❌ API noto‘g‘ri javob qaytardi!")
		return
	}

	body, _ := ioutil.ReadAll(resp.Body)
	reply(bot, msg.Chat.ID, string(body))
}

func reply(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := bot.Send(msg); err != nil {
		log.Println("❌ Xabar yuborishda xatolik:", err)
	}
}
