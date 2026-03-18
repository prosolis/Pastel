package deals

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
)

var storeNames = map[string]string{
	"1":  "Steam",
	"7":  "GOG",
	"11": "Humble Store",
	"23": "GreenManGaming",
}

type CheapSharkDeal struct {
	DealID       string
	GameID       string
	Title        string
	SalePrice    float64
	NormalPrice  float64
	Savings      float64
	DealRating   float64
	StoreID      string
	StoreName    string
	LastChange   int64
	SteamAppID   string
	DealURL      string
	IsHistLow    bool
	DedupID      string
}

type cheapSharkRaw struct {
	DealID     string `json:"dealID"`
	GameID     string `json:"gameID"`
	Title      string `json:"title"`
	SalePrice  string `json:"salePrice"`
	NormalPrice string `json:"normalPrice"`
	Savings    string `json:"savings"`
	DealRating string `json:"dealRating"`
	StoreID    string `json:"storeID"`
	LastChange int64  `json:"lastChange"`
	SteamAppID string `json:"steamAppID"`
}

// FetchCheapSharkDeals fetches deals from CheapShark API.
func FetchCheapSharkDeals(maxPrice float64, pageSize int) ([]CheapSharkDeal, error) {
	url := fmt.Sprintf(
		"https://www.cheapshark.com/api/1.0/deals?storeID=1,7,11,23&upperPrice=%d&sortBy=recent&desc=1&pageSize=%d",
		int(maxPrice), pageSize,
	)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("cheapshark request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cheapshark returned status %d", resp.StatusCode)
	}

	var raw []cheapSharkRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("cheapshark decode failed: %w", err)
	}

	var deals []CheapSharkDeal
	for _, d := range raw {
		sale, _ := strconv.ParseFloat(d.SalePrice, 64)
		normal, _ := strconv.ParseFloat(d.NormalPrice, 64)
		savings, _ := strconv.ParseFloat(d.Savings, 64)
		rating, _ := strconv.ParseFloat(d.DealRating, 64)

		storeName := storeNames[d.StoreID]
		if storeName == "" {
			storeName = "Unknown"
		}

		deals = append(deals, CheapSharkDeal{
			DealID:      d.DealID,
			GameID:      d.GameID,
			Title:       d.Title,
			SalePrice:   sale,
			NormalPrice:  normal,
			Savings:     savings,
			DealRating:  rating,
			StoreID:     d.StoreID,
			StoreName:   storeName,
			LastChange:  d.LastChange,
			SteamAppID:  d.SteamAppID,
			DealURL:     fmt.Sprintf("https://www.cheapshark.com/redirect?dealID=%s", d.DealID),
			DedupID:     fmt.Sprintf("cheapshark-%s-%d", d.GameID, d.LastChange),
		})
	}

	return deals, nil
}

// FilterCheapSharkDeals filters deals by rating, discount, and price thresholds.
func FilterCheapSharkDeals(deals []CheapSharkDeal, minRating float64, minDiscount int, maxPrice float64) []CheapSharkDeal {
	var filtered []CheapSharkDeal
	for _, d := range deals {
		discount := int(math.Floor(d.Savings))
		if discount < minDiscount {
			slog.Debug("cheapshark: skipping (low discount)", "title", d.Title, "discount", discount)
			continue
		}
		if d.DealRating < minRating && d.DealRating != 0 {
			slog.Debug("cheapshark: skipping (low rating)", "title", d.Title, "rating", d.DealRating)
			continue
		}
		if d.SalePrice > maxPrice {
			slog.Debug("cheapshark: skipping (too expensive)", "title", d.Title, "price", d.SalePrice)
			continue
		}
		filtered = append(filtered, d)
	}
	return filtered
}
