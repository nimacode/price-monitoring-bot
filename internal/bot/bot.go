package bot

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"price-monitoring-bot/internal/database"
	"price-monitoring-bot/internal/models"
	"price-monitoring-bot/internal/scheduler"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	API        *tgbotapi.BotAPI
	repo       *database.Repository
	scheduler  *scheduler.Scheduler
	adminID    int64
	sessions   map[int64]*AddSourceSession
	sessionsMu sync.RWMutex
}

type AddSourceSession struct {
	Step       int
	AssetName  string
	Category   models.AssetCategory
	FetchType  string
	URL        string
	Selector   string
	Multiplier float64
	SourceName string
}

func NewBot(token string, repo *database.Repository, scheduler *scheduler.Scheduler, adminID int64) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}

	_, err = api.Request(tgbotapi.DeleteWebhookConfig{})
	if err != nil {
		log.Printf("Warning: Failed to delete webhook: %v", err)
	}

	return &Bot{
		API:       api,
		repo:      repo,
		scheduler: scheduler,
		adminID:   adminID,
		sessions:  make(map[int64]*AddSourceSession),
	}, nil
}

func SetScheduler(b *Bot, s *scheduler.Scheduler) {
	b.scheduler = s
}

func (b *Bot) Start() {
	log.Printf("Bot started as %s", b.API.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.API.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		if update.Message.From.ID != b.adminID {
			b.sendMessage(update.Message.Chat.ID, "شما دسترسی ادمین ندارید.")
			continue
		}

		b.handleMessage(update.Message)
	}
}

func (b *Bot) handleMessage(message *tgbotapi.Message) {
	text := message.Text
	chatID := message.Chat.ID

	if strings.HasPrefix(text, "/") {
		// Allow /cancel to interrupt any active session
		if text == "/cancel" {
			b.cancelSession(chatID)
			return
		}

		// If user is in an active session, treat input as session data (e.g. XPath starting with /)
		b.sessionsMu.RLock()
		_, inSession := b.sessions[chatID]
		b.sessionsMu.RUnlock()

		if inSession {
			b.handleSession(chatID, text)
			return
		}

		b.handleCommand(message)
		return
	}

	b.handleSession(chatID, text)
}

func (b *Bot) handleCommand(message *tgbotapi.Message) {
	text := message.Text
	chatID := message.Chat.ID

	switch {
	case text == "/start":
		b.sendMessage(chatID, "به ربات مانیتورینگ قیمت خوش آمدید!\n\nدستورات موجود:\n/add_source - افزودن سورس جدید\n/force_fetch - دریافت فوری قیمت‌ها\n/force_post - ارسال فوری لیست قیمت‌ها\n/list_sources - لیست سورس‌ها\n/list_assets - لیست دارایی‌ها\n/cancel - لغو عملیات در حال انجام")

	case text == "/add_source":
		b.startAddSourceSession(chatID)

	case text == "/force_fetch":
		b.scheduler.ForceFetch()
		b.sendMessage(chatID, "دریافت قیمت‌ها با موفقیت انجام شد.")

	case text == "/force_post":
		b.scheduler.ForcePost()
		b.sendMessage(chatID, "لیست قیمت‌ها با موفقیت ارسال شد.")

	case text == "/list_sources":
		b.listSources(chatID)

	case text == "/list_assets":
		b.listAssets(chatID)

	case text == "/cancel":
		b.cancelSession(chatID)

	default:
		b.sendMessage(chatID, "دستور نامعتبر است. از /start برای دیدن لیست دستورات استفاده کنید.")
	}
}

func (b *Bot) startAddSourceSession(chatID int64) {
	b.sessionsMu.Lock()
	defer b.sessionsMu.Unlock()

	b.sessions[chatID] = &AddSourceSession{
		Step: 1,
	}

	msg := tgbotapi.NewMessage(chatID, "لطفاً نام دارایی را وارد کنید (مثلاً: USD, BTC, طلا):")
	msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true}
	b.API.Send(msg)
}

func (b *Bot) handleSession(chatID int64, text string) {
	b.sessionsMu.Lock()
	session, exists := b.sessions[chatID]
	b.sessionsMu.Unlock()

	if !exists {
		return
	}

	text = strings.TrimSpace(text)

	switch session.Step {
	case 1:
		session.AssetName = text
		session.Step = 2

		msg := tgbotapi.NewMessage(chatID, "لطفاً دسته‌بندی را انتخاب کنید:\n1. ارز (currency)\n2. طلا (gold)\n3. ارز دیجیتال (crypto)")
		msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true}
		b.API.Send(msg)

	case 2:
		switch strings.ToLower(text) {
		case "ارز", "currency":
			session.Category = models.CurrencyCategory
		case "طلا", "gold":
			session.Category = models.GoldCategory
		case "ارز دیجیتال", "crypto", "کریپتو":
			session.Category = models.CryptoCategory
		default:
			b.sendMessage(chatID, "دسته‌بندی نامعتبر است. لطفاً دوباره تلاش کنید.")
			return
		}

		session.Step = 3

		msg := tgbotapi.NewMessage(chatID, "لطفاً نوع سورس را انتخاب کنید:\n1. API\n2. XPath (Scrape)")
		msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true}
		b.API.Send(msg)

	case 3:
		switch strings.ToLower(text) {
		case "api", "1":
			session.FetchType = "api"
		case "xpath", "scrape", "2":
			session.FetchType = "xpath"
		default:
			b.sendMessage(chatID, "نوع سورس نامعتبر است. لطفاً دوباره تلاش کنید.")
			return
		}

		session.Step = 4

		msg := tgbotapi.NewMessage(chatID, "لطفاً آدرس (URL) سورس را وارد کنید:")
		msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true}
		b.API.Send(msg)

	case 4:
		session.URL = text
		session.Step = 5

		if session.FetchType == "api" {
			msg := tgbotapi.NewMessage(chatID, "لطفاً مسیر JSON را وارد کنید (مثلاً: data.price یا data[0].value):")
			msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true}
			b.API.Send(msg)
		} else {
			msg := tgbotapi.NewMessage(chatID, "لطفاً XPath یا CSS Selector را وارد کنید (مثلاً: .price یا #usd-price):")
			msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true}
			b.API.Send(msg)
		}

	case 5:
		session.Selector = text
		session.Step = 6

		msg := tgbotapi.NewMessage(chatID, "لطفاً ضریب تبدیل را وارد کنید (مثلاً: 100 یا 1):")
		msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true}
		b.API.Send(msg)

	case 6:
		_, err := fmt.Sscanf(text, "%f", &session.Multiplier)
		if err != nil || session.Multiplier <= 0 {
			msg := tgbotapi.NewMessage(chatID, "فرمت نامعتبر است. لطفاً فقط یک عدد مثبت وارد کنید (مثلاً: 100 یا 1):")
			msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true}
			b.API.Send(msg)
			return
		}
		session.Step = 7

		msg := tgbotapi.NewMessage(chatID, "لطفاً نام سورس را وارد کنید (مثلاً: TGJU, Bonbast):")
		msg.ReplyMarkup = tgbotapi.ForceReply{ForceReply: true}
		b.API.Send(msg)

	case 7:
		session.SourceName = text

		if err := b.createAssetAndSource(chatID, session); err != nil {
			b.sendMessage(chatID, fmt.Sprintf("خطا در ایجاد سورس: %v", err))
			b.cancelSession(chatID)
			return
		}

		b.sendMessage(chatID, "سورس با موفقیت ایجاد شد!")
		b.cancelSession(chatID)
	}
}

func (b *Bot) createAssetAndSource(chatID int64, session *AddSourceSession) error {
	assets, err := b.repo.GetAssetsByCategory(session.Category)
	if err != nil {
		return err
	}

	var asset *models.Asset
	for _, a := range assets {
		if strings.EqualFold(a.Name, session.AssetName) {
			asset = &a
			break
		}
	}

	if asset == nil {
		newAsset := &models.Asset{
			Name:          strings.ToUpper(session.AssetName),
			Category:      session.Category,
			AlertThreshold: 0,
			LastSentPrice:  0,
		}

		if err := b.repo.CreateAsset(newAsset); err != nil {
			return fmt.Errorf("خطا در ایجاد دارایی: %w", err)
		}

		asset = newAsset
	}

	source := &models.Source{
		AssetID:    asset.ID,
		SourceName: session.SourceName,
		FetchType:  session.FetchType,
		URL:        session.URL,
		Selector:   session.Selector,
		Multiplier: models.FlexFloat64(session.Multiplier),
		LastVal:    0,
	}

	if err := b.repo.CreateSource(source); err != nil {
		return fmt.Errorf("خطا در ایجاد سورس: %w", err)
	}

	return nil
}

func (b *Bot) listSources(chatID int64) {
	sources, err := b.repo.GetAllSources()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("خطا در دریافت لیست سورس‌ها: %v", err))
		return
	}

	if len(sources) == 0 {
		b.sendMessage(chatID, "هیچ سورسی تعریف نشده است.")
		return
	}

	var builder strings.Builder
	builder.WriteString("لیست سورس‌ها:\n\n")

	for _, source := range sources {
		asset, err := b.repo.GetAssetByID(source.AssetID)
		if err != nil {
			continue
		}

		builder.WriteString(fmt.Sprintf("📌 %s (%s)\n", source.SourceName, asset.Name))
		builder.WriteString(fmt.Sprintf("   نوع: %s\n", source.FetchType))
		builder.WriteString(fmt.Sprintf("   URL: %s\n", source.URL))
		builder.WriteString(fmt.Sprintf("   آخرین مقدار: %.2f\n\n", float64(source.LastVal)))
	}

	b.sendMessage(chatID, builder.String())
}

func (b *Bot) listAssets(chatID int64) {
	assets, err := b.repo.GetAllAssets()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("خطا در دریافت لیست دارایی‌ها: %v", err))
		return
	}

	if len(assets) == 0 {
		b.sendMessage(chatID, "هیچ دارایی تعریف نشده است.")
		return
	}

	var builder strings.Builder
	builder.WriteString("لیست دارایی‌ها:\n\n")

	for _, asset := range assets {
		builder.WriteString(fmt.Sprintf("💎 %s (%s)\n", asset.Name, asset.Category))
		builder.WriteString(fmt.Sprintf("   آستانه هشدار: %.2f%%\n", float64(asset.AlertThreshold)*100))
		builder.WriteString(fmt.Sprintf("   آخرین قیمت ارسالی: %.2f\n\n", float64(asset.LastSentPrice)))
	}

	b.sendMessage(chatID, builder.String())
}

func (b *Bot) cancelSession(chatID int64) {
	b.sessionsMu.Lock()
	defer b.sessionsMu.Unlock()

	delete(b.sessions, chatID)
}

func (b *Bot) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"

	if _, err := b.API.Send(msg); err != nil {
		log.Printf("Error sending message to %d: %v", chatID, err)
	}
}
