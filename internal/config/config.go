package config

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI      string
	DBName        string
	TelegramToken string
	ChannelID     string64
	AdminID       string64
}

type string64 int64

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found, using environment variables")
	}
	
	return &Config{
		MongoURI:      getEnv("MONGO_URI", "mongodb://localhost:27017"),
		DBName:        getEnv("DB_NAME", "price_monitor"),
		TelegramToken: getEnv("TELEGRAM_TOKEN", ""),
		ChannelID:     string64(getEnvInt("CHANNEL_ID", 0)),
		AdminID:       string64(getEnvInt("ADMIN_ID", 0)),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int64 {
	if value := os.Getenv(key); value != "" {
		var result int64
		if _, err := fmt.Sscanf(value, "%d", &result); err == nil {
			return result
		}
	}
	return int64(defaultValue)
}

func (s string64) Int64() int64 {
	return int64(s)
}
