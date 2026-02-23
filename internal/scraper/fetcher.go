package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"price-monitoring-bot/internal/cache"
	"price-monitoring-bot/internal/config"
	"price-monitoring-bot/internal/models"

	"github.com/antchfx/htmlquery"
	"github.com/go-resty/resty/v2"
)

type Fetcher struct {
	client *resty.Client
	cache  *cache.Cache
	cfg    *config.Config
}

func NewFetcher(cfg *config.Config) *Fetcher {
	transport := &http.Transport{
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     30 * time.Second,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
	}

	client := resty.New().
		SetTransport(transport).
		SetTimeout(cfg.ScraperTimeout).
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36").
		SetHeader("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8").
		SetHeader("Accept-Language", "fa-IR,fa;q=0.9,en;q=0.8").
		SetHeader("Accept-Encoding", "gzip, deflate").
		SetRetryCount(cfg.ScraperRetryCount).
		SetRetryWaitTime(cfg.ScraperRetryWait).
		SetRetryMaxWaitTime(cfg.ScraperRetryWait * time.Duration(cfg.ScraperRetryCount)).
		AddRetryCondition(func(r *resty.Response, err error) bool {
			return err != nil || r.StatusCode() >= 500
		})

	return &Fetcher{
		client: client,
		cache:  cache.New(),
		cfg:    cfg,
	}
}

type FetchResult struct {
	SourceID string
	Value    float64
	Error    error
}

func (f *Fetcher) FetchFromAPI(ctx context.Context, url, jsonPath string) (float64, error) {
	cacheKey := fmt.Sprintf("api:%s:%s", url, jsonPath)
	if cached, ok := f.cache.Get(cacheKey); ok {
		if v, ok := cached.(float64); ok {
			return v, nil
		}
	}

	resp, err := f.client.R().SetContext(ctx).Get(url)
	if err != nil {
		return 0, fmt.Errorf("fetch error: %w (url: %s)", err, url)
	}

	if resp.StatusCode() != 200 {
		return 0, fmt.Errorf("http status %d (url: %s)", resp.StatusCode(), url)
	}

	var data interface{}
	if err := json.Unmarshal(resp.Body(), &data); err != nil {
		return 0, fmt.Errorf("json parse error: %w (url: %s)", err, url)
	}

	value, err := getJSONValue(data, jsonPath)
	if err != nil {
		return 0, fmt.Errorf("json path error: %w (path: %s, url: %s)", err, jsonPath, url)
	}

	var result float64
	switch v := value.(type) {
	case float64:
		result = v
	case string:
		result, err = parsePrice(v)
		if err != nil {
			return 0, fmt.Errorf("price parse error: %w (value: %s, url: %s)", err, v, url)
		}
	default:
		return 0, fmt.Errorf("unexpected type %T for price value (url: %s)", value, url)
	}

	f.cache.Set(cacheKey, result, 10*time.Second)
	return result, nil
}

func (f *Fetcher) FetchFromXPath(ctx context.Context, url, xpathExpr string) (float64, error) {
	cacheKey := fmt.Sprintf("xpath:%s:%s", url, xpathExpr)
	if cached, ok := f.cache.Get(cacheKey); ok {
		if v, ok := cached.(float64); ok {
			return v, nil
		}
	}

	resp, err := f.client.R().SetContext(ctx).Get(url)
	if err != nil {
		return 0, fmt.Errorf("fetch error: %w (url: %s)", err, url)
	}

	if resp.StatusCode() != 200 {
		return 0, fmt.Errorf("http status %d (url: %s)", resp.StatusCode(), url)
	}

	doc, err := htmlquery.Parse(strings.NewReader(string(resp.Body())))
	if err != nil {
		return 0, fmt.Errorf("html parse error: %w (url: %s)", err, url)
	}

	node, err := htmlquery.Query(doc, xpathExpr)
	if err != nil {
		return 0, fmt.Errorf("invalid xpath %q: %w (url: %s)", xpathExpr, err, url)
	}
	if node == nil {
		return 0, fmt.Errorf("element not found with xpath: %s (url: %s)", xpathExpr, url)
	}

	text := strings.TrimSpace(htmlquery.InnerText(node))
	text = normalizePriceText(text)

	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("price parse error: %w (text: %q, url: %s)", err, text, url)
	}

	f.cache.Set(cacheKey, value, 10*time.Second)
	return value, nil
}

func (f *Fetcher) Fetch(ctx context.Context, source models.Source) (float64, error) {
	var value float64
	var err error

	switch source.FetchType {
	case "api":
		value, err = f.FetchFromAPI(ctx, source.URL, source.Selector)
	case "xpath":
		value, err = f.FetchFromXPath(ctx, source.URL, source.Selector)
	default:
		err = fmt.Errorf("unknown fetch type: %s", source.FetchType)
	}

	if err == nil && source.Multiplier > 0 {
		value = value * float64(source.Multiplier)
	}

	return value, err
}

func (f *Fetcher) FetchFromSources(ctx context.Context, sources []models.Source) []FetchResult {
	results := make([]FetchResult, len(sources))
	var wg sync.WaitGroup

	semaphore := make(chan struct{}, f.cfg.ScraperConcurrency)

	for i, source := range sources {
		wg.Add(1)
		go func(idx int, s models.Source) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[idx] = FetchResult{
						SourceID: s.ID.Hex(),
						Error:    fmt.Errorf("panic: %v", r),
					}
				}
			}()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			fetchCtx, cancel := context.WithTimeout(ctx, f.cfg.ScraperTimeout)
			defer cancel()

			value, err := f.Fetch(fetchCtx, s)
			results[idx] = FetchResult{
				SourceID: s.ID.Hex(),
				Value:    value,
				Error:    err,
			}
		}(i, source)
	}

	wg.Wait()
	return results
}

func normalizePriceText(text string) string {
	persian := []string{"۰", "۱", "۲", "۳", "۴", "۵", "۶", "۷", "۸", "۹"}
	for i, p := range persian {
		text = strings.ReplaceAll(text, p, strconv.Itoa(i))
	}
	var b strings.Builder
	for _, r := range text {
		if (r >= '0' && r <= '9') || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func getJSONValue(data interface{}, path string) (interface{}, error) {
	parts := strings.Split(path, ".")
	current := data

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		switch v := current.(type) {
		case map[string]interface{}:
			if val, ok := v[part]; ok {
				current = val
			} else {
				return nil, fmt.Errorf("key not found: %s", part)
			}
		case []interface{}:
			index, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid array index: %s", part)
			}
			if index < 0 || index >= len(v) {
				return nil, fmt.Errorf("array index out of range: %d", index)
			}
			current = v[index]
		default:
			return nil, fmt.Errorf("cannot traverse into non-container type")
		}
	}

	return current, nil
}

func parsePrice(text string) (float64, error) {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, ",", "")
	text = strings.ReplaceAll(text, " ", "")
	return strconv.ParseFloat(text, 64)
}
