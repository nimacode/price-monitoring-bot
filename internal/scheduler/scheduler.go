package scheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"price-monitoring-bot/internal/config"
	"price-monitoring-bot/internal/database"
	"price-monitoring-bot/internal/logger"
	"price-monitoring-bot/internal/models"
	"price-monitoring-bot/internal/scraper"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/robfig/cron/v3"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Scheduler struct {
	cron      *cron.Cron
	repo      *database.Repository
	fetcher   *scraper.Fetcher
	bot       *tgbotapi.BotAPI
	channelID int64
	alertChan chan AlertMessage
	cfg       *config.Config
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
}

type AlertMessage struct {
	Category  models.AssetCategory
	Message   string
	Timestamp time.Time
}

func NewScheduler(cfg *config.Config, repo *database.Repository, fetcher *scraper.Fetcher, bot *tgbotapi.BotAPI, channelID int64) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		cron:      cron.New(cron.WithSeconds()),
		repo:      repo,
		fetcher:   fetcher,
		bot:       bot,
		channelID: channelID,
		alertChan: make(chan AlertMessage, 100),
		cfg:       cfg,
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (s *Scheduler) Start() {
	s.cron.AddFunc(s.cfg.SchedulerFetchCron, s.fetchData)
	s.cron.AddFunc(s.cfg.SchedulerPostCron, s.publishSummary)
	s.cron.Start()

	s.wg.Add(1)
	go s.processAlerts()

	logger.Info("Scheduler started")
}

func (s *Scheduler) Stop() {
	s.cancel()
	s.cron.Stop()
	close(s.alertChan)
	s.wg.Wait()
	logger.Info("Scheduler stopped")
}

func (s *Scheduler) fetchData() {
	ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer cancel()

	logger.Info("Fetching data from all sources...")

	sources, err := s.repo.GetAllSources(ctx)
	if err != nil {
		logger.Error("Error fetching sources: %v", err)
		return
	}

	results := s.fetcher.FetchFromSources(ctx, sources)

	var wg sync.WaitGroup
	for _, result := range results {
		if result.Error != nil {
			logger.Error("Error fetching from source %s: %v", result.SourceID, result.Error)
			continue
		}

		sourceID, err := primitive.ObjectIDFromHex(result.SourceID)
		if err != nil {
			logger.Error("Error parsing source ID: %v", err)
			continue
		}

		wg.Add(1)
		go func(id primitive.ObjectID, val float64) {
			defer wg.Done()
			if err := s.repo.UpdateSourceValue(ctx, id, val); err != nil {
				logger.Error("Error updating source value: %v", err)
			}
			s.checkAlerts(id.Hex(), val)
		}(sourceID, result.Value)
	}
	wg.Wait()
}

func (s *Scheduler) publishSummary() {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	logger.Info("Publishing price summary...")

	assets, err := s.repo.GetAllAssets(ctx)
	if err != nil {
		logger.Error("Error fetching assets: %v", err)
		return
	}

	categories := map[models.AssetCategory][]models.Asset{
		models.GoldCategory:     {},
		models.CurrencyCategory: {},
		models.CryptoCategory:   {},
	}

	for _, asset := range assets {
		categories[asset.Category] = append(categories[asset.Category], asset)
	}

	var wg sync.WaitGroup
	for category, categoryAssets := range categories {
		if len(categoryAssets) == 0 {
			continue
		}

		wg.Add(1)
		go func(cat models.AssetCategory, assets []models.Asset) {
			defer wg.Done()
			message := s.buildCategoryMessage(ctx, cat, assets, false)
			s.sendMessage(message)

			for _, asset := range assets {
				avgPrice := s.getAveragePrice(ctx, asset.ID.Hex())
				s.repo.UpdateAssetLastSentPrice(ctx, asset.ID, avgPrice)
			}
		}(category, categoryAssets)
	}
	wg.Wait()
}

func (s *Scheduler) ForcePost() {
	s.publishSummary()
}

func (s *Scheduler) ForceFetch() {
	s.fetchData()
}

func (s *Scheduler) checkAlerts(sourceID string, currentValue float64) {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	sources, err := s.repo.GetAllSources(ctx)
	if err != nil {
		return
	}

	var source *models.Source
	for i := range sources {
		if sources[i].ID.Hex() == sourceID {
			source = &sources[i]
			break
		}
	}

	if source == nil {
		return
	}

	asset, err := s.repo.GetAssetByID(ctx, source.AssetID)
	if err != nil {
		return
	}

	if asset.LastSentPrice == 0 {
		return
	}

	lastSentPrice := float64(asset.LastSentPrice)
	threshold := float64(asset.AlertThreshold)
	if threshold == 0 {
		switch asset.Category {
		case models.GoldCategory, models.CurrencyCategory:
			threshold = s.cfg.AlertThresholdGold
		case models.CryptoCategory:
			threshold = s.cfg.AlertThresholdCrypto
		}
	}

	changePercent := math.Abs((currentValue - lastSentPrice) / lastSentPrice)

	if changePercent >= threshold {
		s.triggerAlert(ctx, asset.Category)
	}
}

func (s *Scheduler) triggerAlert(ctx context.Context, category models.AssetCategory) {
	assets, err := s.repo.GetAssetsByCategory(ctx, category)
	if err != nil {
		return
	}

	if len(assets) == 0 {
		return
	}

	message := s.buildCategoryMessage(ctx, category, assets, true)
	select {
	case s.alertChan <- AlertMessage{
		Category:  category,
		Message:   message,
		Timestamp: time.Now(),
	}:
	default:
		logger.Error("Alert channel full, dropping alert")
	}
}

func (s *Scheduler) processAlerts() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case alert, ok := <-s.alertChan:
			if !ok {
				return
			}
			s.sendMessage(alert.Message)
			time.Sleep(5 * time.Second)
		}
	}
}

func (s *Scheduler) buildCategoryMessage(ctx context.Context, category models.AssetCategory, assets []models.Asset, isAlert bool) string {
	var builder strings.Builder

	var emoji, title string
	switch category {
	case models.GoldCategory:
		emoji = "💰"
		title = "طلا"
	case models.CurrencyCategory:
		emoji = "💵"
		title = "ارز"
	case models.CryptoCategory:
		emoji = "₿"
		title = "ارز دیجیتال"
	}

	if isAlert {
		builder.WriteString(fmt.Sprintf("⚠️ هشدار نوسان %s\n\n", title))
	} else {
		builder.WriteString(fmt.Sprintf("%s قیمت لحظه‌ای %s\n\n", emoji, title))
	}

	sort.Slice(assets, func(i, j int) bool {
		return assets[i].Name < assets[j].Name
	})

	for _, asset := range assets {
		sources, err := s.repo.GetSourcesByAssetID(ctx, asset.ID)
		if err != nil || len(sources) == 0 {
			continue
		}

		for _, source := range sources {
			if source.LastVal == 0 {
				continue
			}

			priceStr := formatPrice(float64(source.LastVal))
			timeAgo := time.Since(source.UpdatedAt)
			timeStr := formatDuration(timeAgo)

			builder.WriteString(fmt.Sprintf("🔸 %s: %s (سورس: %s - %s پیش)\n", asset.Name, priceStr, source.SourceName, timeStr))
		}
	}

	return builder.String()
}

func (s *Scheduler) getAveragePrice(ctx context.Context, assetID string) float64 {
	sources, err := s.repo.GetSourcesByAssetIDHex(ctx, assetID)
	if err != nil || len(sources) == 0 {
		return 0
	}

	var sum float64
	count := 0

	for _, source := range sources {
		if source.LastVal > 0 {
			sum += float64(source.LastVal)
			count++
		}
	}

	if count == 0 {
		return 0
	}

	return sum / float64(count)
}

func (s *Scheduler) sendMessage(text string) {
	msg := tgbotapi.NewMessage(s.channelID, text)
	msg.ParseMode = "HTML"

	if _, err := s.bot.Send(msg); err != nil {
		logger.Error("Error sending message: %v", err)
	}
}

func formatPrice(price float64) string {
	if price == 0 {
		return "0"
	}
	s := fmt.Sprintf("%.0f", price)
	n := len(s)
	if n <= 3 {
		return s + " تومان"
	}
	var result []byte
	for i := 0; i < n; i++ {
		if i > 0 && (n-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, s[i])
	}
	return string(result) + " تومان"
}

func formatDuration(d time.Duration) string {
	minutes := int(d.Minutes())

	if minutes < 1 {
		return "کمتر از ۱ دقیقه"
	} else if minutes < 60 {
		return fmt.Sprintf("%d دقیقه", minutes)
	} else if minutes < 1440 {
		hours := minutes / 60
		return fmt.Sprintf("%d ساعت", hours)
	} else {
		days := minutes / 1440
		return fmt.Sprintf("%d روز", days)
	}
}
