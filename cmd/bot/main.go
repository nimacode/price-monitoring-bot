package main

import (
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"price-monitoring-bot/internal/bot"
	"price-monitoring-bot/internal/config"
	"price-monitoring-bot/internal/database"
	"price-monitoring-bot/internal/scheduler"
	"price-monitoring-bot/internal/scraper"
)

func main() {
	logPath := os.Getenv("LOG_FILE")
	if logPath == "" {
		logPath = "logs/bot.log"
	}

	if err := os.MkdirAll("logs", 0755); err == nil {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			log.SetOutput(io.MultiWriter(os.Stdout, f))
		}
	}
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	log.Println("Starting Price Monitoring Bot...")

	cfg := config.Load()

	if cfg.TelegramToken == "" {
		log.Fatal("TELEGRAM_TOKEN environment variable is required")
	}

	if cfg.ChannelID.Int64() == 0 {
		log.Fatal("CHANNEL_ID environment variable is required")
	}

	if cfg.AdminID.Int64() == 0 {
		log.Fatal("ADMIN_ID environment variable is required")
	}

	mongoDB, err := database.NewMongoDB(cfg.MongoURI, cfg.DBName)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer mongoDB.Close()

	repo := database.NewRepository(mongoDB)

	fetcher := scraper.NewFetcher()

	telegramBot, err := bot.NewBot(cfg.TelegramToken, repo, nil, cfg.AdminID.Int64())
	if err != nil {
		log.Fatalf("Failed to create Telegram bot: %v", err)
	}

	scheduler := scheduler.NewScheduler(repo, fetcher, telegramBot.API, cfg.ChannelID.Int64())
	bot.SetScheduler(telegramBot, scheduler)

	scheduler.Start()

	go telegramBot.Start()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan

	log.Println("Shutting down gracefully...")
	scheduler.Stop()
	log.Println("Bot stopped.")
}
