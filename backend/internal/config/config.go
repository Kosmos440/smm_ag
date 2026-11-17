package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"
)

type Config struct {
	DatabaseURL     string
	HTTPAddr        string
	TelegramToken   string
	CollectInterval time.Duration
	TickInterval    time.Duration
}

var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		HTTPAddr:      os.Getenv("HTTP_ADDR"),
		TelegramToken: strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = "127.0.0.1:8180"
	}
	if cfg.TelegramToken != "" && !telegramTokenPattern.MatchString(cfg.TelegramToken) {
		return Config{}, errors.New("invalid TELEGRAM_BOT_TOKEN")
	}
	var err error
	cfg.CollectInterval, err = interval("COLLECT_INTERVAL", 2*time.Minute)
	if err != nil {
		return Config{}, err
	}
	cfg.TickInterval, err = interval("WORKER_TICK_INTERVAL", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func interval(key string, fallback time.Duration) (time.Duration, error) {
	if value := os.Getenv(key); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration < time.Second {
			return 0, fmt.Errorf("%s must be a duration of at least 1s", key)
		}
		return duration, nil
	}
	return fallback, nil
}
