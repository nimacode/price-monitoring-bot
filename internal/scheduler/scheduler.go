package scheduler

import (
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"price-monitoring-bot/internal/database"
	"price-monitoring-bot/internal/models"
	"price-monitoring-bot/internal/scraper"

	"github.com/robfig/cron/v3"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Scheduler struct {
	cron        *cron.Cron
	repo        *database.Repository
	fetcher     *scraper.Fetcher
	bot         *tgbotapi.BotAPI
	channelID   int64
	alertChan   chan AlertMessage
}

type AlertMessage struct {
	Category  models.AssetCategory
	Message   string
	Timestamp time.Time
}

func NewScheduler(repo *database.Repository, fetcher *scraper.Fetcher, bot *tgbotapi.BotAPI, channelID int64) *Scheduler {
	return &Scheduler{
		cron:      cron.New(cron.WithSeconds()),
		repo:      repo,
		fetcher:   fetcher,
		bot:       bot,
		channelID: channelID,
		alertChan: make(chan AlertMessage, 100),
	}
}

func (s *Scheduler) Start() {
	s.cron.AddFunc("0 */10 * * * *", s.fetchData)
	s.cron.AddFunc("0 */30 * * * *", s.publishSummary)
	s.cron.Start()
	
	go s.processAlerts()
	
	log.Println("Scheduler started")
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
	close(s.alertChan)
	log.Println("Scheduler stopped")
}

func (s *Scheduler) fetchData() {
	log.Println("Fetching data from all sources...")
	
	sources, err := s.repo.GetAllSources()
	if err != nil {
		log.Printf("Error fetching sources: %v", err)
		return
	}
	
	results := s.fetcher.FetchFromSources(sources)
	
	for _, result := range results {
		if result.Error != nil {
			log.Printf("Error fetching from source %s: %v", result.SourceID, result.Error)
			continue
		}
		
		sourceID, err := primitive.ObjectIDFromHex(result.SourceID)
		if err != nil {
			log.Printf("Error parsing source ID: %v", err)
			continue
		}
		
		if err := s.repo.UpdateSourceValue(sourceID, result.Value); err != nil {
			log.Printf("Error updating source value: %v", err)
			continue
		}
		
		s.checkAlerts(sourceID.Hex(), result.Value)
	}
}

func (s *Scheduler) publishSummary() {
	log.Println("Publishing price summary...")
	
	assets, err := s.repo.GetAllAssets()
	if err != nil {
		log.Printf("Error fetching assets: %v", err)
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
	
	for category, assets := range categories {
		if len(assets) == 0 {
			continue
		}
		
		message := s.buildCategoryMessage(category, assets, false)
		s.sendMessage(message)
		
		for _, asset := range assets {
			avgPrice := s.getAveragePrice(asset.ID.Hex())
			s.repo.UpdateAssetLastSentPrice(asset.ID, avgPrice)
		}
	}
}

func (s *Scheduler) ForcePost() {
	s.publishSummary()
}

func (s *Scheduler) ForceFetch() {
	s.fetchData()
}

func (s *Scheduler) checkAlerts(sourceID string, currentValue float64) {
	sources, err := s.repo.GetAllSources()
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
	
	asset, err := s.repo.GetAssetByID(source.AssetID)
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
			threshold = 0.01
		case models.CryptoCategory:
			threshold = 0.03
		}
	}

	changePercent := math.Abs((currentValue - lastSentPrice) / lastSentPrice)

	if changePercent >= threshold {
		s.triggerAlert(asset.Category)
	}
}

func (s *Scheduler) triggerAlert(category models.AssetCategory) {
	assets, err := s.repo.GetAssetsByCategory(category)
	if err != nil {
		return
	}
	
	if len(assets) == 0 {
		return
	}
	
	message := s.buildCategoryMessage(category, assets, true)
	s.alertChan <- AlertMessage{
		Category:  category,
		Message:   message,
		Timestamp: time.Now(),
	}
}

func (s *Scheduler) processAlerts() {
	for alert := range s.alertChan {
		s.sendMessage(alert.Message)
		time.Sleep(5 * time.Second)
	}
}

func (s *Scheduler) buildCategoryMessage(category models.AssetCategory, assets []models.Asset, isAlert bool) string {
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
		sources, err := s.repo.GetSourcesByAssetID(asset.ID)
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

func (s *Scheduler) getAveragePrice(assetID string) float64 {
	sources, err := s.repo.GetSourcesByAssetIDWithObjectID(assetID)
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
		log.Printf("Error sending message: %v", err)
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
