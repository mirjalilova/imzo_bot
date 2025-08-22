package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// === Config ===

type Config struct {
	TelegramToken     string
	ImzoAPIBase       string
	ImzoChatRoomID    string
	GatewayBase       string
	GatewayAuthBearer string
	PollInterval      time.Duration
	PollTimeout       time.Duration
	HTTPTimeout       time.Duration
}

func mustConfig() Config {
	c := Config{
		TelegramToken:     os.Getenv("TELEGRAM_BOT_TOKEN"),
		ImzoAPIBase:       os.Getenv("IMZO_API_BASE"),
		ImzoChatRoomID:    os.Getenv("IMZO_CHAT_ROOM_ID"),
		GatewayBase:       os.Getenv("GATEWAY_BASE"),
		GatewayAuthBearer: os.Getenv("GATEWAY_AUTH_BEARER"),
	}
	if c.TelegramToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN is required")
	}
	if c.ImzoAPIBase == "" {
		c.ImzoAPIBase = "https://imzo-ai.uzjoylar.uz"
	}
	if c.ImzoChatRoomID == "" {
		log.Fatal("IMZO_CHAT_ROOM_ID is required")
	}
	if c.GatewayBase == "" {
		c.GatewayBase = "http://localhost:8080"
	}
	if v := os.Getenv("POLL_INTERVAL_SECONDS"); v != "" {
		if d, err := time.ParseDuration(v + "s"); err == nil {
			c.PollInterval = d
		}
	}
	if c.PollInterval == 0 {
		c.PollInterval = 3 * time.Second
	}
	if v := os.Getenv("POLL_TIMEOUT_SECONDS"); v != "" {
		if d, err := time.ParseDuration(v + "s"); err == nil {
			c.PollTimeout = d
		}
	}
	if c.PollTimeout == 0 {
		c.PollTimeout = 120 * time.Second
	}
	if v := os.Getenv("HTTP_TIMEOUT_SECONDS"); v != "" {
		if d, err := time.ParseDuration(v + "s"); err == nil {
			c.HTTPTimeout = d
		}
	}
	if c.HTTPTimeout == 0 {
		c.HTTPTimeout = 20 * time.Second
	}
	return c
}

// === Imzo API payloads ===

type loginReq struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type loginResp struct {
	Message string `json:"message"`
	Token   string `json:"token"`
}

type askReq struct {
	ChatRoomID string `json:"chat_room_id"`
	Request    string `json:"request"`
}

type askRespOK struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type askRespErr struct {
	Message string `json:"message"`
	Status  string `json:"status"`
}

type finalResp struct {
	Response string `json:"responce"` // API returns "responce"
}

// === Per-user session state ===

type userState int

const (
	stateIdle userState = iota
	stateAwaitLogin
	stateAwaitPassword
	stateReady
)

type session struct {
	State      userState
	LoginCache string
	Token      string
}

// === Bot ===

type Bot struct {
	cfg  Config
	bot  *tgbotapi.BotAPI
	cli  *http.Client
	smux sync.RWMutex
	sess map[int64]*session // by Telegram chat ID
}

func newBot(cfg Config) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, err
	}
	api.Debug = false
	return &Bot{
		cfg:  cfg,
		bot:  api,
		cli:  &http.Client{Timeout: cfg.HTTPTimeout},
		sess: make(map[int64]*session),
	}, nil
}

func (b *Bot) getSession(chatID int64) *session {
	b.smux.Lock()
	defer b.smux.Unlock()
	s, ok := b.sess[chatID]
	if !ok {
		s = &session{State: stateIdle}
		b.sess[chatID] = s
	}
	return s
}

func (b *Bot) setState(chatID int64, st userState) {
	s := b.getSession(chatID)
	s.State = st
}

func (b *Bot) setToken(chatID int64, token string) {
	s := b.getSession(chatID)
	s.Token = token
}

func (b *Bot) setLogin(chatID int64, login string) {
	s := b.getSession(chatID)
	s.LoginCache = login
}

func (b *Bot) Run(ctx context.Context) error {
	updCfg := tgbotapi.NewUpdate(0)
	updCfg.Timeout = 60
	updates := b.bot.GetUpdatesChan(updCfg)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case u := <-updates:
			if u.Message == nil { // ignore non-message updates
				continue
			}
			chatID := u.Message.Chat.ID
			text := strings.TrimSpace(u.Message.Text)

			if strings.HasPrefix(text, "/start") {
				b.handleStart(chatID)
				continue
			}

			s := b.getSession(chatID)
			switch s.State {
			case stateAwaitLogin:
				b.setLogin(chatID, text)
				b.reply(chatID, "Parolni yuboring:")
				b.setState(chatID, stateAwaitPassword)
			case stateAwaitPassword:
				if err := b.doLogin(chatID, s.LoginCache, text); err != nil {
					b.reply(chatID, fmt.Sprintf("Login xato: %v\nQayta urinib ko'ring: /start", err))
					b.setState(chatID, stateIdle)
					break
				}
				b.reply(chatID, "✅ Muvaffaqiyatli! Endi savolingizni yuboring.")
				b.setState(chatID, stateReady)
			case stateReady:
				if s.Token == "" {
					b.reply(chatID, "Avval /start orqali login qiling.")
					b.setState(chatID, stateIdle)
					break
				}
				b.handleQuestion(ctx, chatID, s.Token, text)
			default:
				b.handleStart(chatID)
			}
		}
	}
}

func (b *Bot) handleStart(chatID int64) {
	b.reply(chatID, "Assalomu alaykum! Telefon raqamingizni yuboring (masalan: +998901234567):")
	b.setState(chatID, stateAwaitLogin)
}

func (b *Bot) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := b.bot.Send(msg); err != nil {
		log.Printf("telegram send error: %v", err)
	}
}

// === HTTP helpers ===

func (b *Bot) doLogin(chatID int64, login, password string) error {
	endpoint := strings.TrimRight(b.cfg.ImzoAPIBase, "/") + "/users/login"
	payload := loginReq{Login: login, Password: password}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}
	var lr loginResp
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return err
	}
	if lr.Token == "" {
		return errors.New("empty token in login response")
	}
	b.setToken(chatID, lr.Token)
	return nil
}

func (b *Bot) handleQuestion(ctx context.Context, chatID int64, token, question string) {
	endpoint := strings.TrimRight(b.cfg.ImzoAPIBase, "/") + "/ask"
	payload := askReq{ChatRoomID: b.cfg.ImzoChatRoomID, Request: question}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)

	resp, err := b.cli.Do(req)
	if err != nil {
		b.reply(chatID, fmt.Sprintf("Xatolik: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		// 400 case: forward error message to user and ask for new message
		var er askRespErr
		if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
			b.reply(chatID, fmt.Sprintf("400 xatolik, lekin JSON o'qilmadi: %v", err))
			return
		}
		if er.Message == "" {
			er.Message = "Uzr, savolingizni tushunmadim. Qayta yuboring."
		}
		b.reply(chatID, er.Message+"\n\n*Yangi savolingizni yuboring.*")
		return
	}

	fmt.Println(resp)
	if resp.StatusCode != http.StatusOK {
		b.reply(chatID, fmt.Sprintf("Unexpected status from /ask: %s", resp.Status))
		return
	}

	var ok askRespOK
	if err := json.NewDecoder(resp.Body).Decode(&ok); err != nil {
		b.reply(chatID, fmt.Sprintf("JSON xatosi (/ask): %v", err))
		return
	}
	fmt.Println("aaaaaaaaaaaaa", ok.Message)

	if strings.TrimSpace(ok.Message) != "" {
		b.reply(chatID, ok.Message)
	}

	time.Sleep(10 * time.Second)
	// Empty message => tell user it's being processed, then poll for final answer.
	// b.reply(chatID, "⌛ Savolingiz qabul qilindi. Tez orada javob qaytadi — ishlov berilmoqda…")
	go b.pollFinalAndReply(chatID, token, ok.ID)
}

func (b *Bot) pollFinalAndReply(chatID int64, userToken, id string) {
	log.Printf("[DEBUG] Polling started for chatID=%d id=%s", chatID, id)

	ctx, cancel := context.WithTimeout(context.Background(), b.cfg.PollTimeout)
	defer cancel()

	endpoint := strings.TrimRight(b.cfg.ImzoAPIBase, "/") + "/get/gpt/responce"
	q := url.Values{"id": {id}}

	authHeader := strings.TrimSpace(b.cfg.GatewayAuthBearer)
	if authHeader == "" {
		authHeader = userToken
	}

	ticker := time.NewTicker(b.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[DEBUG] Polling timeout expired")
			// b.reply(chatID, "⏱️ Kutish vaqti tugadi. Iltimos, yana urinib ko'ring yoki savolni yangilang.")
			return

		case <-ticker.C:
			log.Println("[DEBUG] Sending poll request...")
			req, err := http.NewRequest(http.MethodGet, endpoint+"?"+q.Encode(), nil)
			if err != nil {
				log.Printf("poll request create error: %v", err)
				continue
			}
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Authorization", authHeader)

			resp, err := b.cli.Do(req)
			if err != nil {
				log.Printf("poll request error: %v", err)
				continue
			}

			func() {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					log.Printf("poll status not OK: %s", resp.Status)
					return
				}

				var fr finalResp
				if err := json.NewDecoder(resp.Body).Decode(&fr); err != nil {
					log.Printf("poll decode error: %v", err)
					return
				}

				log.Printf("[DEBUG] Poll response: %+v", fr)

				if strings.TrimSpace(fr.Response) != "" {
					b.reply(chatID, fr.Response)
					log.Println("[DEBUG] Final response received, stopping polling")
					cancel() // ctx cancel qilinadi
					return   // funksiya shu joyda tugaydi
				}
			}()
		}
	}
}

func main() {
	cfg := mustConfig()
	b, err := newBot(cfg)
	if err != nil {
		log.Fatalf("bot init: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Println("Bot ishga tushdi…")
	if err := b.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("bot run: %v", err)
	}
	log.Println("Bot to'xtadi.")
}
