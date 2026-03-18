package currency

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	apiURL   = "https://api.frankfurter.dev/v1/latest?base=USD&symbols=CAD,EUR,GBP"
	cacheTTL = 12 * time.Hour
)

var symbols = map[string]string{
	"USD": "$",
	"CAD": "C$",
	"EUR": "€",
	"GBP": "£",
}

var currencies = []string{"CAD", "EUR", "GBP"}

type Converter struct {
	mu          sync.Mutex
	rates       map[string]float64
	lastFetched time.Time
}

func NewConverter() *Converter {
	return &Converter{}
}

func (c *Converter) fetchRates() error {
	resp, err := http.Get(apiURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("frankfurter API returned status %d", resp.StatusCode)
	}

	var result struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	c.rates = result.Rates
	c.lastFetched = time.Now()
	return nil
}

// EnsureRates fetches exchange rates if the cache is stale or empty.
func (c *Converter) EnsureRates() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.lastFetched) < cacheTTL && c.rates != nil {
		return
	}

	if err := c.fetchRates(); err != nil {
		slog.Warn("failed to fetch exchange rates, using USD only", "error", err)
	}
}

// FormatPrice converts a USD amount to multi-currency display string.
func (c *Converter) FormatPrice(usd float64) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	parts := []string{fmt.Sprintf("$%.2f", usd)}

	for _, cur := range currencies {
		if rate, ok := c.rates[cur]; ok {
			sym := symbols[cur]
			parts = append(parts, fmt.Sprintf("%s%.2f", sym, usd*rate))
		}
	}

	return strings.Join(parts, " · ")
}

// HasRates returns true if exchange rates are available.
func (c *Converter) HasRates() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rates != nil && len(c.rates) > 0
}
