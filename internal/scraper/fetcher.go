package scraper

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"price-monitoring-bot/internal/models"

	"github.com/antchfx/htmlquery"
	"github.com/go-resty/resty/v2"
)

type Fetcher struct {
	client *resty.Client
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		client: resty.New().
			SetTimeout(15 * time.Second).
			SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36").
			SetHeader("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8").
			SetHeader("Accept-Language", "fa-IR,fa;q=0.9,en;q=0.8"),
	}
}

type FetchResult struct {
	SourceID string
	Value    float64
	Error    error
}

func (f *Fetcher) FetchFromAPI(url, jsonPath string) (float64, error) {
	resp, err := f.client.R().Get(url)
	if err != nil {
		return 0, err
	}

	var data interface{}
	if err := json.Unmarshal(resp.Body(), &data); err != nil {
		return 0, err
	}

	value, err := getJSONValue(data, jsonPath)
	if err != nil {
		return 0, err
	}

	switch v := value.(type) {
	case float64:
		return v, nil
	case string:
		fv, err := parsePrice(v)
		if err != nil {
			return 0, err
		}
		return fv, nil
	default:
		return 0, fmt.Errorf("unexpected type for price value")
	}
}

func (f *Fetcher) FetchFromXPath(url, xpathExpr string) (float64, error) {
	resp, err := f.client.R().Get(url)
	if err != nil {
		return 0, err
	}

	doc, err := htmlquery.Parse(strings.NewReader(string(resp.Body())))
	if err != nil {
		return 0, fmt.Errorf("failed to parse HTML: %w", err)
	}

	node := htmlquery.FindOne(doc, xpathExpr)
	if node == nil {
		return 0, fmt.Errorf("no elements found with selector: %s", xpathExpr)
	}

	text := strings.TrimSpace(htmlquery.InnerText(node))
	text = normalizePriceText(text)

	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse price %q: %w", text, err)
	}

	return value, nil
}

// normalizePriceText converts Persian digits to English and strips non-numeric characters.
func normalizePriceText(text string) string {
	persian := []string{"۰", "۱", "۲", "۳", "۴", "۵", "۶", "۷", "۸", "۹"}
	for i, p := range persian {
		text = strings.ReplaceAll(text, p, strconv.Itoa(i))
	}
	// Remove everything except digits and dot
	var b strings.Builder
	for _, r := range text {
		if (r >= '0' && r <= '9') || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (f *Fetcher) FetchFromSources(sources []models.Source) []FetchResult {
	var wg sync.WaitGroup
	results := make([]FetchResult, len(sources))
	resultChan := make(chan FetchResult, len(sources))

	for _, source := range sources {
		wg.Add(1)
		go func(s models.Source) {
			defer wg.Done()
			var value float64
			var err error

			if s.FetchType == "api" {
				value, err = f.FetchFromAPI(s.URL, s.Selector)
			} else if s.FetchType == "xpath" {
				value, err = f.FetchFromXPath(s.URL, s.Selector)
			} else {
				err = fmt.Errorf("unknown fetch type: %s", s.FetchType)
			}

			if err == nil && s.Multiplier > 0 {
				value = value * float64(s.Multiplier)
			}

			resultChan <- FetchResult{
				SourceID: s.ID.Hex(),
				Value:    value,
				Error:    err,
			}
		}(source)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	i := 0
	for result := range resultChan {
		results[i] = result
		i++
	}

	return results
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
