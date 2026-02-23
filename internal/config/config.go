package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI      string
	DBName        string
	TelegramToken string
	ChannelID     int64
	AdminID       int64
	LogFile       string
	Debug         bool

	ScraperConcurrency   int
	ScraperTimeout       time.Duration
	ScraperRetryCount    int
	ScraperRetryWait     time.Duration
	SchedulerFetchCron   string
	SchedulerPostCron    string
	AlertThresholdGold   float64
	AlertThresholdCrypto float64
	AlertThresholdFiat   float64
}

func Load() *Config {
	godotenv.Load()

	return &Config{
		MongoURI:      buildMongoURI(),
		DBName:        getEnv("DB_NAME", "price_monitor"),
		TelegramToken: getEnv("TELEGRAM_TOKEN", ""),
		ChannelID:     getEnvInt("CHANNEL_ID", 0),
		AdminID:       getEnvInt("ADMIN_ID", 0),
		LogFile:       getEnv("LOG_FILE", "logs/bot.log"),
		Debug:         getEnvBool("DEBUG", false),

		ScraperConcurrency:   int(getEnvInt("SCRAPER_CONCURRENCY", 10)),
		ScraperTimeout:       getEnvDuration("SCRAPER_TIMEOUT", 20*time.Second),
		ScraperRetryCount:    int(getEnvInt("SCRAPER_RETRY_COUNT", 3)),
		ScraperRetryWait:     getEnvDuration("SCRAPER_RETRY_WAIT", 2*time.Second),
		SchedulerFetchCron:   getEnv("SCHEDULER_FETCH_CRON", "0 */10 * * * *"),
		SchedulerPostCron:    getEnv("SCHEDULER_POST_CRON", "0 */30 * * * *"),
		AlertThresholdGold:   getEnvFloat("ALERT_THRESHOLD_GOLD", 0.01),
		AlertThresholdCrypto: getEnvFloat("ALERT_THRESHOLD_CRYPTO", 0.03),
		AlertThresholdFiat:   getEnvFloat("ALERT_THRESHOLD_FIAT", 0.01),
	}
}

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
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
	}
	return int64(defaultValue)
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1"
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}
