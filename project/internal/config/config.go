package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port           string
	DatabaseURL    string
	CheckInterval  time.Duration
	MaxConcurrency int
	HTTPTimeout    time.Duration
	ShutdownGrace  time.Duration
}

func Load() *Config {
	return &Config{
		Port:           getEnv("PORT", "8080"),
		DatabaseURL:    getEnv("DATABASE_URL", ""),
		CheckInterval:  getDuration("CHECK_INTERVAL", 15*time.Second),
		MaxConcurrency: getInt("MAX_CONCURRENCY", 8),
		HTTPTimeout:    getDuration("HTTP_TIMEOUT", 5*time.Second),
		ShutdownGrace:  getDuration("SHUTDOWN_GRACE", 10*time.Second),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getInt(key string, defaultValue int) int {
	if str := os.Getenv(key); str != "" {
		if value, err := strconv.Atoi(str); err == nil {
			return value
		}
	}
	return defaultValue
}

func getDuration(key string, defaultValue time.Duration) time.Duration {
	if str := os.Getenv(key); str != "" {
		if value, err := time.ParseDuration(str); err == nil {
			return value
		}
	}
	return defaultValue
}
