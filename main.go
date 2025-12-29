package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/gorilla/websocket"
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

type verifyReq struct {
	PhoneNumber string `json:"phone_number"`
	Code        int    `json:"code"`
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

const (
	ModeFast  = "FAST"
	ModeDeep  = "DEEP"
	ModeLegal = "DOCUMENT_EXPERT"
	ModeWeb   = "WEB"
)

// === Per-user session state ===

type userState int

const (
	stateIdle userState = iota
	stateAwaitPhone
	stateAwaitPassword
	stateAwaitVerifyCode
	stateReady
)

type session struct {
	State      userState
	LoginCache string
	Token      string
	Mode       string 
}

// === Bot ===

type Bot struct {
	cfg  Config
	bot  *tgbotapi.BotAPI
	cli  *http.Client
	smux sync.RWMutex
	sess map[int64]*session 
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
		s = &session{
			State: stateIdle,
			Mode:  ModeFast, // ✅ default
		}
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
			if u.Message == nil {
				continue
			}
			chatID := u.Message.Chat.ID
			text := strings.TrimSpace(u.Message.Text)

			if strings.HasPrefix(text, "/start") {
				b.handleStart(chatID)
				continue
			}
			if strings.HasPrefix(text, "/mode") {
				b.handleMode(chatID, text)
				continue
			}


			s := b.getSession(chatID)
			switch s.State {
			case stateAwaitPhone:
				phone := text

				if !isValidPhone(phone) {
					b.reply(chatID, "❌ Telefon formati noto‘g‘ri. Masalan: +998901234567")
					continue

				}

				b.setLogin(chatID, phone)

				role, err := b.checkUserRole(phone)
				if err != nil {
					logErr("role check error: %v", err)
					b.reply(chatID, "❌ Xatolik yuz berdi")
					b.setState(chatID, stateIdle)
					continue

				}

				if role == "admin" {
					b.reply(chatID, "🔐 Parolni yuboring:")
					b.setState(chatID, stateAwaitPassword)
				} else {
					b.reply(chatID,
						"Tasdiqlash kodni shu yerga yuboring.\n\n"+
						"ℹ️ Kodni [@ai_imzo_bot](https://t.me/ai_imzo_bot) orqali olasiz",
					)
					b.setState(chatID, stateAwaitVerifyCode)
				}

			case stateAwaitVerifyCode:
				code, err := strconv.Atoi(text)
				if err != nil {
					b.reply(chatID, "❌ Kod faqat raqam bo‘lishi kerak")
					continue
				}

				if err := b.doVerifyCode(chatID, s.LoginCache, code); err != nil {
					b.reply(chatID, "❌ Kod noto‘g‘ri yoki eskirgan")
					b.setState(chatID, stateIdle)
					continue
				}

				b.reply(chatID, "✅ Tasdiqlandi! Endi savolingizni yuboring.")
				b.setState(chatID, stateReady)


			case stateAwaitPassword:
				if err := b.doLogin(chatID, s.LoginCache, text); err != nil {
					b.reply(chatID, "Login xato, Qayta urinib ko'ring: /start")
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

func (b *Bot) handleMode(chatID int64, text string) {
	s := b.getSession(chatID)
	parts := strings.Fields(strings.ToLower(text))

	if len(parts) == 1 {
		b.reply(chatID,
			fmt.Sprintf(
				"🎛 *Joriy mode:* `%s`\n\n"+
					"Mode tanlash:\n"+
					"`/mode fast`  — Tezkor javob\n"+
					"`/mode deep`  — Chuqur tahlil\n"+
					"`/mode legal` — Hujjat eksperti\n"+
					"`/mode web`   — Web qidiruv\n",
				s.Mode,
			),
		)
		return
	}

	var mode string

	switch parts[1] {
	case "fast":
		mode = ModeFast
	case "deep":
		mode = ModeDeep
	case "legal":
		mode = ModeLegal
	case "web":
		mode = ModeWeb
	default:
		b.reply(chatID, "❌ Noto‘g‘ri mode. Masalan: `/mode fast`")
		return
	}

	s.Mode = mode

	b.reply(chatID,
		fmt.Sprintf("✅ Mode o‘zgartirildi: *%s*", mode),
	)
}

func (b *Bot) doVerifyCode(chatID int64, phone string, code int) error {
	endpoint := b.cfg.ImzoAPIBase + "/users/verify-code"

	payload := verifyReq{
		PhoneNumber: phone,
		Code:        code,
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.cli.Do(req)
	if err != nil {
		logErr("verify request error: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logWarn("verify failed with status: %s", resp.Status)
		return errors.New("verify failed")
	}

	logInfo("Verify success for %s", phone)

	b.setToken(chatID, "verified-user")
	return nil
}



func (b *Bot) handleStart(chatID int64) {
	b.reply(chatID, "📱 Telefon raqamingizni yuboring (+998...)")
	b.setState(chatID, stateAwaitPhone)
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
	logInfo("Admin login success (token via cookie)")
	b.setToken(chatID, "admin-session")
	return nil
}

func (b *Bot) handleQuestion(ctx context.Context, chatID int64, token, question string) {
	wsURL := strings.Replace(
		b.cfg.ImzoAPIBase,
		"https://",
		"wss://",
		1,
	) + "/ws/" + b.cfg.ImzoChatRoomID

	header := http.Header{}
	header.Set("Origin", "https://imzo-ai.uzjoylar.uz")

	if token != "" {
		header.Set("Authorization", token)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		b.reply(chatID, "❌ Serverga ulanishda xatolik")
		logErr("ws dial error: %v", err)
		return
	}
	defer conn.Close()

	s := b.getSession(chatID)

	req := map[string]any{
		"message": question,
		"mode":    s.Mode,
	}

	if err := conn.WriteJSON(req); err != nil {
		logErr("ws write error: %v", err)
		return
	}

	var full strings.Builder

	for {
		select {
		case <-ctx.Done():
			return
		default:
			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				logWarn("ws read closed: %v", err)
				return
			}

			switch msg["type"] {
			case "chunk":
				if chunk, ok := msg["data"].(string); ok {
					full.WriteString(chunk)
				}

			case "gpt":
				if res, ok := msg["response"].(string); ok {
					b.reply(chatID, res)
					return
				}

			case "error":
				b.reply(chatID, "❌ Xatolik yuz berdi")
				return
			}
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

func logInfo(msg string, args ...any) {
	log.Printf("[INFO] "+msg, args...)
}

func logWarn(msg string, args ...any) {
	log.Printf("[WARN] "+msg, args...)
}

func logErr(msg string, args ...any) {
	log.Printf("[ERROR] "+msg, args...)
}

func isValidPhone(phone string) bool {
	return strings.HasPrefix(phone, "+998") && len(phone) == 13
}

func (b *Bot) checkUserRole(phone string) (string, error) {
	adminPhones := map[string]bool{
		"+998947777777": true,
	}

	if adminPhones[phone] {
		logInfo("Admin detected: %s", phone)
		return "admin", nil
	}

	logInfo("Regular user detected: %s", phone)
	return "user", nil
}
