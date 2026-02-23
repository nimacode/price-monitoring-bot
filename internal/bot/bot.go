package bot

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"price-monitoring-bot/internal/config"
	"price-monitoring-bot/internal/database"
	"price-monitoring-bot/internal/logger"
	"price-monitoring-bot/internal/models"
	"price-monitoring-bot/internal/scheduler"
	"price-monitoring-bot/internal/scraper"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Bot struct {
	API            *tgbotapi.BotAPI
	repo           *database.Repository
	scheduler      *scheduler.Scheduler
	cfg            *config.Config
	sessions       map[int64]*AddSourceSession
	sessionsMu     sync.RWMutex
	editSessions   map[int64]*EditSourceSession
	editSessionsMu sync.RWMutex
	sourceListPage map[int64]int
	sourceListMu   sync.RWMutex
	sourceListMode map[int64]string
}

const sourcesPerPage = 8

type EditSourceSession struct {
	SourceID primitive.ObjectID
	Field    string
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

func NewBot(cfg *config.Config, repo *database.Repository, scheduler *scheduler.Scheduler) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, err
	}

	_, err = api.Request(tgbotapi.DeleteWebhookConfig{})
	if err != nil {
		logger.Error("Warning: Failed to delete webhook: %v", err)
	}

	return &Bot{
		API:            api,
		repo:           repo,
		scheduler:      scheduler,
		cfg:            cfg,
		sessions:       make(map[int64]*AddSourceSession),
		editSessions:   make(map[int64]*EditSourceSession),
		sourceListPage: make(map[int64]int),
		sourceListMode: make(map[int64]string),
	}, nil
}

func SetScheduler(b *Bot, s *scheduler.Scheduler) {
	b.scheduler = s
}

func (b *Bot) Start() {
	logger.Info("Bot started as %s", b.API.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.API.GetUpdatesChan(u)

	for update := range updates {
		if update.CallbackQuery != nil {
			if update.CallbackQuery.From.ID != b.cfg.AdminID {
				b.API.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "دسترسی ندارید"))
				continue
			}
			b.handleCallback(update.CallbackQuery)
			continue
		}

		if update.Message == nil {
			continue
		}

		if update.Message.From.ID != b.cfg.AdminID {
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
		if text == "/cancel" {
			b.cancelSession(chatID)
			b.cancelEditSession(chatID)
			b.sendMenu(chatID)
			return
		}

		b.sessionsMu.RLock()
		_, inSession := b.sessions[chatID]
		b.sessionsMu.RUnlock()

		if inSession {
			b.handleSession(chatID, text)
			return
		}

		b.editSessionsMu.RLock()
		_, inEdit := b.editSessions[chatID]
		b.editSessionsMu.RUnlock()

		if inEdit {
			b.handleEditSession(chatID, text)
			return
		}

		b.handleCommand(message)
		return
	}

	b.editSessionsMu.RLock()
	_, inEdit := b.editSessions[chatID]
	b.editSessionsMu.RUnlock()

	if inEdit {
		b.handleEditSession(chatID, text)
		return
	}

	b.handleSession(chatID, text)
}

func (b *Bot) sendMenu(chatID int64) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ افزودن سورس جدید", "add_source"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️ ویرایش سورس", "edit_source_menu"),
			tgbotapi.NewInlineKeyboardButtonData("🗑 حذف سورس", "delete_source_menu"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 دریافت فوری قیمت‌ها", "force_fetch"),
			tgbotapi.NewInlineKeyboardButtonData("📤 ارسال فوری قیمت‌ها", "force_post"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 لیست سورس‌ها", "list_sources"),
			tgbotapi.NewInlineKeyboardButtonData("💎 لیست دارایی‌ها", "list_assets"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, "🤖 پنل مدیریت ربات")
	msg.ReplyMarkup = keyboard
	b.API.Send(msg)
}

func (b *Bot) handleCallback(cb *tgbotapi.CallbackQuery) {
	chatID := cb.Message.Chat.ID
	b.API.Request(tgbotapi.NewCallback(cb.ID, ""))

	switch cb.Data {
	case "add_source":
		b.startAddSourceSession(chatID)
	case "force_fetch":
		b.scheduler.ForceFetch()
		b.sendMessage(chatID, "✅ دریافت قیمت‌ها با موفقیت انجام شد.")
	case "force_post":
		b.scheduler.ForcePost()
		b.sendMessage(chatID, "✅ ارسال قیمت‌ها با موفقیت انجام شد.")
	case "list_sources":
		b.listSources(chatID)
	case "list_assets":
		b.listAssets(chatID)
	case "edit_source_menu":
		b.showSourceListForEdit(chatID, 0)
	case "delete_source_menu":
		b.showSourceListForDelete(chatID, 0)
	case "cancel":
		b.cancelSession(chatID)
		b.cancelEditSession(chatID)
		b.sendMenu(chatID)
	case "noop":
	case "cat_currency":
		b.handleSessionCallback(chatID, string(models.CurrencyCategory))
	case "cat_gold":
		b.handleSessionCallback(chatID, string(models.GoldCategory))
	case "cat_crypto":
		b.handleSessionCallback(chatID, string(models.CryptoCategory))
	case "ft_api":
		b.handleSessionCallback(chatID, "api")
	case "ft_xpath":
		b.handleSessionCallback(chatID, "xpath")
	default:
		data := cb.Data
		switch {
		case strings.HasPrefix(data, "src_page:"):
			parts := strings.Split(strings.TrimPrefix(data, "src_page:"), ":")
			if len(parts) == 3 {
				page, _ := strconv.Atoi(parts[0])
				mode := parts[1]
				if mode == "edit" {
					b.showSourceListForEdit(chatID, page)
				} else {
					b.showSourceListForDelete(chatID, page)
				}
			}
		case strings.HasPrefix(data, "edit_src:"):
			b.handleEditSourceMenu(chatID, strings.TrimPrefix(data, "edit_src:"))
		case strings.HasPrefix(data, "del_src:"):
			b.handleDeleteSourceConfirm(chatID, strings.TrimPrefix(data, "del_src:"))
		case strings.HasPrefix(data, "edit_field:"):
			rest := strings.TrimPrefix(data, "edit_field:")
			idx := strings.LastIndex(rest, ":")
			if idx > 0 {
				b.startEditFieldSession(chatID, rest[:idx], rest[idx+1:])
			}
		case strings.HasPrefix(data, "del_confirm:"):
			b.deleteSource(chatID, strings.TrimPrefix(data, "del_confirm:"))
		}
	}
}

func (b *Bot) handleSessionCallback(chatID int64, value string) {
	b.sessionsMu.RLock()
	_, exists := b.sessions[chatID]
	b.sessionsMu.RUnlock()

	if !exists {
		b.sendMenu(chatID)
		return
	}

	b.handleSession(chatID, value)
}

func (b *Bot) handleCommand(message *tgbotapi.Message) {
	chatID := message.Chat.ID

	switch message.Text {
	case "/start":
		b.sendMenu(chatID)
	case "/add_source":
		b.startAddSourceSession(chatID)
	case "/force_fetch":
		b.scheduler.ForceFetch()
		b.sendMessage(chatID, "✅ دریافت قیمت‌ها با موفقیت انجام شد.")
	case "/force_post":
		b.scheduler.ForcePost()
		b.sendMessage(chatID, "✅ ارسال قیمت‌ها با موفقیت انجام شد.")
	case "/list_sources":
		b.listSources(chatID)
	case "/list_assets":
		b.listAssets(chatID)
	case "/cancel":
		b.cancelSession(chatID)
		b.sendMenu(chatID)
	case "/test_fetch":
		b.testFetch(chatID)
	default:
		if strings.HasPrefix(message.Text, "/test_url ") {
			b.testURL(chatID, strings.TrimPrefix(message.Text, "/test_url "))
		} else {
			b.sendMessage(chatID, "دستور نامعتبر است.")
		}
	}
}

func (b *Bot) startAddSourceSession(chatID int64) {
	b.sessionsMu.Lock()
	defer b.sessionsMu.Unlock()

	b.sessions[chatID] = &AddSourceSession{
		Step: 1,
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ انصراف", "cancel"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, "لطفاً نام دارایی را وارد کنید (مثلاً: USD, BTC, طلا):")
	msg.ReplyMarkup = kb
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

		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("💵 ارز", "cat_currency"),
				tgbotapi.NewInlineKeyboardButtonData("🏅 طلا", "cat_gold"),
				tgbotapi.NewInlineKeyboardButtonData("₿ ارز دیجیتال", "cat_crypto"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ انصراف", "cancel"),
			),
		)
		msg := tgbotapi.NewMessage(chatID, "دسته‌بندی دارایی را انتخاب کنید:")
		msg.ReplyMarkup = kb
		b.API.Send(msg)

	case 2:
		var cat models.AssetCategory
		switch strings.ToLower(text) {
		case string(models.CurrencyCategory):
			cat = models.CurrencyCategory
		case string(models.GoldCategory):
			cat = models.GoldCategory
		case string(models.CryptoCategory):
			cat = models.CryptoCategory
		default:
			b.sendMessage(chatID, "دسته‌بندی نامعتبر است.")
			return
		}
		session.Category = cat
		session.Step = 3

		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔌 API (JSON)", "ft_api"),
				tgbotapi.NewInlineKeyboardButtonData("🌐 XPath (Scrape)", "ft_xpath"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ انصراف", "cancel"),
			),
		)
		msg := tgbotapi.NewMessage(chatID, "نوع دریافت داده را انتخاب کنید:")
		msg.ReplyMarkup = kb
		b.API.Send(msg)

	case 3:
		switch strings.ToLower(text) {
		case "api":
			session.FetchType = "api"
		case "xpath":
			session.FetchType = "xpath"
		default:
			b.sendMessage(chatID, "نوع نامعتبر است.")
			return
		}

		session.Step = 4

		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ انصراف", "cancel"),
			),
		)
		msg := tgbotapi.NewMessage(chatID, "لطفاً آدرس (URL) سورس را وارد کنید:")
		msg.ReplyMarkup = kb
		b.API.Send(msg)

	case 4:
		session.URL = text
		session.Step = 5

		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ انصراف", "cancel"),
			),
		)
		var prompt string
		if session.FetchType == "api" {
			prompt = "لطفاً مسیر JSON را وارد کنید (مثلاً: data.price یا data[0].value):"
		} else {
			prompt = "لطفاً XPath را وارد کنید (مثلاً: //*[@id=\"price\"]):"
		}
		msg := tgbotapi.NewMessage(chatID, prompt)
		msg.ReplyMarkup = kb
		b.API.Send(msg)

	case 5:
		session.Selector = text
		session.Step = 6

		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ انصراف", "cancel"),
			),
		)
		msg := tgbotapi.NewMessage(chatID, "لطفاً ضریب تبدیل را وارد کنید (مثلاً: 100 یا 1):")
		msg.ReplyMarkup = kb
		b.API.Send(msg)

	case 6:
		_, err := fmt.Sscanf(text, "%f", &session.Multiplier)
		if err != nil || session.Multiplier <= 0 {
			b.sendMessage(chatID, "فرمت نامعتبر است. لطفاً فقط یک عدد مثبت وارد کنید (مثلاً: 100 یا 1):")
			return
		}
		session.Step = 7

		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ انصراف", "cancel"),
			),
		)
		msg := tgbotapi.NewMessage(chatID, "لطفاً نام سورس را وارد کنید (مثلاً: TGJU, Bonbast):")
		msg.ReplyMarkup = kb
		b.API.Send(msg)

	case 7:
		session.SourceName = text

		if err := b.createAssetAndSource(chatID, session); err != nil {
			b.sendMessage(chatID, fmt.Sprintf("خطا در ایجاد سورس: %v", err))
			b.cancelSession(chatID)
			b.sendMenu(chatID)
			return
		}

		b.sendMessage(chatID, "✅ سورس با موفقیت ایجاد شد!")
		b.cancelSession(chatID)
		b.sendMenu(chatID)
	}
}

func (b *Bot) createAssetAndSource(chatID int64, session *AddSourceSession) error {
	ctx := context.Background()

	assets, err := b.repo.GetAssetsByCategory(ctx, session.Category)
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
			Name:           strings.ToUpper(session.AssetName),
			Category:       session.Category,
			AlertThreshold: 0,
			LastSentPrice:  0,
		}

		if err := b.repo.CreateAsset(ctx, newAsset); err != nil {
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

	if err := b.repo.CreateSource(ctx, source); err != nil {
		return fmt.Errorf("خطا در ایجاد سورس: %w", err)
	}

	return nil
}

func (b *Bot) listSources(chatID int64) {
	ctx := context.Background()
	sources, err := b.repo.GetAllSources(ctx)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("خطا در دریافت لیست سورس‌ها: %v", err))
		return
	}

	if len(sources) == 0 {
		b.sendMessage(chatID, "هیچ سورسی تعریف نشده است.")
		return
	}

	var builder strings.Builder
	builder.WriteString("📋 <b>لیست تمام سورس‌ها:</b>\n\n")

	for i, source := range sources {
		asset, err := b.repo.GetAssetByID(ctx, source.AssetID)
		assetName := "نامشخص"
		if err == nil {
			assetName = asset.Name
		}
		builder.WriteString(fmt.Sprintf("%d. <b>%s</b> (%s) - %s\n", i+1, source.SourceName, assetName, source.FetchType))
	}

	b.sendMessage(chatID, builder.String())
}

func (b *Bot) showSourceListForEdit(chatID int64, page int) {
	ctx := context.Background()
	sources, err := b.repo.GetAllSources(ctx)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("خطا در دریافت لیست سورس‌ها: %v", err))
		return
	}

	if len(sources) == 0 {
		b.sendMessage(chatID, "هیچ سورسی تعریف نشده است.")
		return
	}

	b.sourceListMu.Lock()
	b.sourceListPage[chatID] = page
	b.sourceListMode[chatID] = "edit"
	b.sourceListMu.Unlock()

	totalPages := (len(sources) + sourcesPerPage - 1) / sourcesPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	start := page * sourcesPerPage
	end := start + sourcesPerPage
	if end > len(sources) {
		end = len(sources)
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := start; i < end; i++ {
		source := sources[i]
		asset, _ := b.repo.GetAssetByID(ctx, source.AssetID)
		assetName := "نامشخص"
		if asset != nil {
			assetName = asset.Name
		}
		btnText := fmt.Sprintf("📝 %s (%s)", source.SourceName, assetName)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, "edit_src:"+source.ID.Hex()),
		))
	}

	navRow := []tgbotapi.InlineKeyboardButton{}
	if page > 0 {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ قبلی", fmt.Sprintf("src_page:%d:edit:prev", page-1)))
	}
	navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "noop"))
	if page < totalPages-1 {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("بعدی ➡️", fmt.Sprintf("src_page:%d:edit:next", page+1)))
	}
	rows = append(rows, navRow)
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🏠 بازگشت به منو", "cancel"),
	))

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("✏️ <b>انتخاب سورس برای ویرایش:</b>\n\nتعداد کل: %d", len(sources)))
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.API.Send(msg)
}

func (b *Bot) showSourceListForDelete(chatID int64, page int) {
	ctx := context.Background()
	sources, err := b.repo.GetAllSources(ctx)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("خطا در دریافت لیست سورس‌ها: %v", err))
		return
	}

	if len(sources) == 0 {
		b.sendMessage(chatID, "هیچ سورسی تعریف نشده است.")
		return
	}

	b.sourceListMu.Lock()
	b.sourceListPage[chatID] = page
	b.sourceListMode[chatID] = "delete"
	b.sourceListMu.Unlock()

	totalPages := (len(sources) + sourcesPerPage - 1) / sourcesPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	start := page * sourcesPerPage
	end := start + sourcesPerPage
	if end > len(sources) {
		end = len(sources)
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := start; i < end; i++ {
		source := sources[i]
		asset, _ := b.repo.GetAssetByID(ctx, source.AssetID)
		assetName := "نامشخص"
		if asset != nil {
			assetName = asset.Name
		}
		btnText := fmt.Sprintf("🗑 %s (%s)", source.SourceName, assetName)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, "del_src:"+source.ID.Hex()),
		))
	}

	navRow := []tgbotapi.InlineKeyboardButton{}
	if page > 0 {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ قبلی", fmt.Sprintf("src_page:%d:delete:prev", page-1)))
	}
	navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "noop"))
	if page < totalPages-1 {
		navRow = append(navRow, tgbotapi.NewInlineKeyboardButtonData("بعدی ➡️", fmt.Sprintf("src_page:%d:delete:next", page+1)))
	}
	rows = append(rows, navRow)
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🏠 بازگشت به منو", "cancel"),
	))

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("🗑 <b>انتخاب سورس برای حذف:</b>\n\nتعداد کل: %d", len(sources)))
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.API.Send(msg)
}

func (b *Bot) handleEditSourceMenu(chatID int64, sourceIDHex string) {
	ctx := context.Background()
	oid, err := primitive.ObjectIDFromHex(sourceIDHex)
	if err != nil {
		b.sendMessage(chatID, "شناسه سورس نامعتبر است.")
		return
	}
	source, err := b.repo.GetSourceByID(ctx, oid)
	if err != nil {
		b.sendMessage(chatID, "سورس یافت نشد.")
		return
	}

	asset, _ := b.repo.GetAssetByID(ctx, source.AssetID)
	assetName := "نامشخص"
	if asset != nil {
		assetName = asset.Name
	}

	text := fmt.Sprintf(
		"✏️ <b>ویرایش سورس: %s</b>\n"+
			"دارایی: <b>%s</b>\n"+
			"نوع: <code>%s</code>\n"+
			"URL: <code>%s</code>\n"+
			"Selector: <code>%s</code>\n"+
			"ضریب: <code>%.4g</code>\n\n"+
			"کدام فیلد را ویرایش می‌کنید؟",
		source.SourceName, assetName, source.FetchType, source.URL, source.Selector, float64(source.Multiplier),
	)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📛 نام", "edit_field:"+sourceIDHex+":source_name"),
			tgbotapi.NewInlineKeyboardButtonData("🔗 URL", "edit_field:"+sourceIDHex+":url"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔍 Selector", "edit_field:"+sourceIDHex+":selector"),
			tgbotapi.NewInlineKeyboardButtonData("✖️ ضریب", "edit_field:"+sourceIDHex+":multiplier"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔌 نوع (API/XPath)", "edit_field:"+sourceIDHex+":fetch_type"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 بازگشت به لیست", "edit_source_menu"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 منو اصلی", "cancel"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = kb
	b.API.Send(msg)
}

func (b *Bot) handleDeleteSourceConfirm(chatID int64, sourceIDHex string) {
	ctx := context.Background()
	oid, err := primitive.ObjectIDFromHex(sourceIDHex)
	if err != nil {
		b.sendMessage(chatID, "شناسه سورس نامعتبر است.")
		return
	}
	source, err := b.repo.GetSourceByID(ctx, oid)
	if err != nil {
		b.sendMessage(chatID, "سورس یافت نشد.")
		return
	}

	asset, _ := b.repo.GetAssetByID(ctx, source.AssetID)
	assetName := "نامشخص"
	if asset != nil {
		assetName = asset.Name
	}

	text := fmt.Sprintf(
		"⚠️ <b>تأیید حذف سورس</b>\n\n"+
			"نام: <b>%s</b>\n"+
			"دارایی: <b>%s</b>\n"+
			"نوع: <code>%s</code>\n"+
			"URL: <code>%s</code>\n\n"+
			"آیا مطمئن هستید؟",
		source.SourceName, assetName, source.FetchType, source.URL,
	)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ بله، حذف کن", "del_confirm:"+sourceIDHex),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 بازگشت به لیست", "delete_source_menu"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 منو اصلی", "cancel"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = kb
	b.API.Send(msg)
}

func (b *Bot) deleteSource(chatID int64, sourceIDHex string) {
	ctx := context.Background()
	oid, err := primitive.ObjectIDFromHex(sourceIDHex)
	if err != nil {
		b.sendMessage(chatID, "شناسه سورس نامعتبر است.")
		return
	}
	if err := b.repo.DeleteSource(ctx, oid); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("خطا در حذف: %v", err))
		return
	}
	b.sendMessage(chatID, "✅ سورس با موفقیت حذف شد.")
	b.showSourceListForDelete(chatID, 0)
}

func (b *Bot) startEditFieldSession(chatID int64, sourceIDHex, field string) {
	oid, err := primitive.ObjectIDFromHex(sourceIDHex)
	if err != nil {
		b.sendMessage(chatID, "شناسه سورس نامعتبر است.")
		return
	}

	b.editSessionsMu.Lock()
	b.editSessions[chatID] = &EditSourceSession{SourceID: oid, Field: field}
	b.editSessionsMu.Unlock()

	fieldLabels := map[string]string{
		"source_name": "نام سورس",
		"url":         "آدرس URL",
		"selector":    "Selector / XPath",
		"multiplier":  "ضریب تبدیل",
		"fetch_type":  "نوع دریافت (api / xpath)",
	}
	label := fieldLabels[field]
	if label == "" {
		label = field
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 انصراف", "edit_src:"+sourceIDHex),
		),
	)
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("مقدار جدید برای <b>%s</b> را وارد کنید:", label))
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = kb
	b.API.Send(msg)
}

func (b *Bot) handleEditSession(chatID int64, text string) {
	ctx := context.Background()
	b.editSessionsMu.Lock()
	session, exists := b.editSessions[chatID]
	if !exists {
		b.editSessionsMu.Unlock()
		return
	}
	sourceID := session.SourceID
	delete(b.editSessions, chatID)
	b.editSessionsMu.Unlock()

	text = strings.TrimSpace(text)

	var value interface{}
	if session.Field == "multiplier" {
		mult, err := strconv.ParseFloat(text, 64)
		if err != nil || mult <= 0 {
			b.editSessionsMu.Lock()
			b.editSessions[chatID] = &EditSourceSession{SourceID: sourceID, Field: session.Field}
			b.editSessionsMu.Unlock()
			b.sendMessage(chatID, "فرمت نامعتبر است. باید یک عدد مثبت باشد.")
			return
		}
		value = mult
	} else if session.Field == "fetch_type" {
		t := strings.ToLower(text)
		if t != "api" && t != "xpath" {
			b.editSessionsMu.Lock()
			b.editSessions[chatID] = &EditSourceSession{SourceID: sourceID, Field: session.Field}
			b.editSessionsMu.Unlock()
			b.sendMessage(chatID, "مقدار نامعتبر. فقط api یا xpath.")
			return
		}
		value = t
	} else {
		value = text
	}

	if err := b.repo.UpdateSourceField(ctx, session.SourceID, session.Field, value); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("خطا در ویرایش: %v", err))
		return
	}

	b.sendMessage(chatID, "✅ سورس با موفقیت ویرایش شد.")
	b.handleEditSourceMenu(chatID, sourceID.Hex())
}

func (b *Bot) cancelEditSession(chatID int64) {
	b.editSessionsMu.Lock()
	defer b.editSessionsMu.Unlock()
	delete(b.editSessions, chatID)
}

func (b *Bot) listAssets(chatID int64) {
	ctx := context.Background()
	assets, err := b.repo.GetAllAssets(ctx)
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

func (b *Bot) testFetch(chatID int64) {
	ctx := context.Background()
	sources, err := b.repo.GetAllSources(ctx)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("خطا در دریافت سورس‌ها: %v", err))
		return
	}

	if len(sources) == 0 {
		b.sendMessage(chatID, "هیچ سورسی وجود ندارد.")
		return
	}

	fetcher := scraper.NewFetcher(b.cfg)
	var builder strings.Builder
	builder.WriteString("🧪 <b>تست Fetch:</b>\n\n")

	for _, source := range sources {
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		value, err := fetcher.Fetch(fetchCtx, source)
		cancel()

		if err != nil {
			builder.WriteString(fmt.Sprintf("❌ %s: <code>%v</code>\n", source.SourceName, err))
		} else {
			builder.WriteString(fmt.Sprintf("✅ %s: <code>%.2f</code>\n", source.SourceName, value))
		}
	}

	b.sendMessage(chatID, builder.String())
}

func (b *Bot) testURL(chatID int64, urlStr string) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil
		},
	}

	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("خطا در ایجاد درخواست: %v", err))
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ <b>خطا در اتصال:</b>\n<code>%v</code>", err))
		return
	}
	defer resp.Body.Close()

	b.sendMessage(chatID, fmt.Sprintf("✅ <b>وضعیت:</b> %d %s\n<b>Content-Length:</b> %d\n<b>Content-Type:</b> %s",
		resp.StatusCode, resp.Status, resp.ContentLength, resp.Header.Get("Content-Type")))
}
