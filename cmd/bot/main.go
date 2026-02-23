package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"price-monitoring-bot/internal/bot"
	"price-monitoring-bot/internal/config"
	"price-monitoring-bot/internal/database"
	"price-monitoring-bot/internal/logger"
	"price-monitoring-bot/internal/scheduler"
	"price-monitoring-bot/internal/scraper"
)

func main() {
	cfg := config.Load()

	log := logger.Init(cfg.LogFile, cfg.Debug)

	log.Info("Starting Price Monitoring Bot...")

	if cfg.TelegramToken == "" {
		log.Error("TELEGRAM_TOKEN environment variable is required")
		os.Exit(1)
	}

	if cfg.ChannelID == 0 {
		log.Error("CHANNEL_ID environment variable is required")
		os.Exit(1)
	}

	if cfg.AdminID == 0 {
		log.Error("ADMIN_ID environment variable is required")
		os.Exit(1)
	}

	mongoDB, err := database.NewMongoDB(cfg)
	if err != nil {
		log.Error("Failed to connect to MongoDB: %v", err)
		os.Exit(1)
	}
	defer mongoDB.Close()

	ctx := context.Background()
	repo := database.NewRepository(mongoDB)
	if err := repo.CreateIndexes(ctx); err != nil {
		log.Error("Failed to create indexes: %v", err)
	}

	fetcher := scraper.NewFetcher(cfg)

	telegramBot, err := bot.NewBot(cfg, repo, nil)
	if err != nil {
		log.Error("Failed to create Telegram bot: %v", err)
		os.Exit(1)
	}

	sched := scheduler.NewScheduler(cfg, repo, fetcher, telegramBot.API, cfg.ChannelID)
	bot.SetScheduler(telegramBot, sched)

	sched.Start()

	go telegramBot.Start()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan

	log.Info("Shutting down gracefully...")
	sched.Stop()
	log.Info("Bot stopped.")
}
