package config

import (
	"log"
	"os"
	"strconv"
)

type Config struct {
	TelegramBotToken   string
	ImzoAPIBase        string
	ImzoChatRoomID     string
	PollIntervalSec    int
	PollTimeoutSec     int
	GatewayBase        string
	GatewayAuthBearer  string
	HTTPTimeoutSeconds int
}

func Load() *Config {
	cfg := &Config{
		TelegramBotToken:   getEnv("TELEGRAM_BOT_TOKEN", ""),
		ImzoAPIBase:        getEnv("IMZO_API_BASE", ""),
		ImzoChatRoomID:     getEnv("IMZO_CHAT_ROOM_ID", ""),
		GatewayBase:        getEnv("GATEWAY_BASE", "http://localhost:8080"),
		GatewayAuthBearer:  getEnv("GATEWAY_AUTH_BEARER", ""),
	}

	cfg.PollIntervalSec = getEnvAsInt("POLL_INTERVAL_SECONDS", 3)
	cfg.PollTimeoutSec = getEnvAsInt("POLL_TIMEOUT_SECONDS", 120)
	cfg.HTTPTimeoutSeconds = getEnvAsInt("HTTP_TIMEOUT_SECONDS", 20)

	return cfg
}

func getEnv(key, defaultVal string) string {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	return val
}

func getEnvAsInt(key string, defaultVal int) int {
	valStr := os.Getenv(key)
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		log.Printf("⚠️ Warning: %s must be int, using default %d\n", key, defaultVal)
		return defaultVal
	}
	return val
}
