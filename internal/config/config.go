package config

import (
	"fmt"
	"log"
	"net/url"
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

	mongoURI := buildMongoURI()

	return &Config{
		MongoURI:      mongoURI,
		DBName:        getEnv("DB_NAME", "price_monitor"),
		TelegramToken: getEnv("TELEGRAM_TOKEN", ""),
		ChannelID:     string64(getEnvInt("CHANNEL_ID", 0)),
		AdminID:       string64(getEnvInt("ADMIN_ID", 0)),
	}
}

// buildMongoURI constructs a properly URL-encoded MongoDB connection URI.
// If MONGO_USER and MONGO_PASSWORD are set, they take priority over MONGO_URI.
func buildMongoURI() string {
	user := os.Getenv("MONGO_USER")
	pass := os.Getenv("MONGO_PASSWORD")
	db := getEnv("MONGO_DB", getEnv("DB_NAME", "price_monitor"))

	if user != "" && pass != "" {
		return fmt.Sprintf("mongodb://%s:%s@mongo:27017/%s?authSource=admin",
			url.QueryEscape(user),
			url.QueryEscape(pass),
			db,
		)
	}

	return getEnv("MONGO_URI", "mongodb://localhost:27017")
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
