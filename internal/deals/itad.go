package deals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type ITADDeal struct {
	GameID    string
	Slug      string
	Title     string
	Type      string
	Discount  int
	Price     float64
	Regular   float64
	Currency  string
	ShopName  string
	ShopID    int
	URL       string
	Flag      string // "H" = historical low, "N" = new low
	Timestamp time.Time
	Expiry    *time.Time
	DedupID   string
	IsHistLow bool
}

type itadResponse struct {
	List []itadEntry `json:"list"`
}

type itadEntry struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Type  string `json:"type"`
	Deal  struct {
		Shop struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"shop"`
		Price struct {
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
		} `json:"price"`
		Regular struct {
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
		} `json:"regular"`
		Cut       int     `json:"cut"`
		URL       string  `json:"url"`
		Flag      string  `json:"flag"`
		Timestamp string  `json:"timestamp"`
		Expiry    *string `json:"expiry"`
	} `json:"deal"`
}

// FetchITADDeals fetches deals from ITAD API v2.
func FetchITADDeals(apiKey string, limit int) ([]ITADDeal, error) {
	if limit > 200 {
		limit = 200
	}

	url := fmt.Sprintf(
		"https://api.isthereanydeal.com/deals/v2?key=%s&sort=-cut&limit=%d&nondeals=false",
		apiKey, limit,
	)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("itad request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("itad returned status %d", resp.StatusCode)
	}

	var result itadResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("itad decode failed: %w", err)
	}

	var deals []ITADDeal
	for _, e := range result.List {
		// Only include games and DLC
		t := strings.ToLower(e.Type)
		if t != "game" && t != "dlc" {
			continue
		}

		deal := ITADDeal{
			GameID:   e.ID,
			Slug:     e.Slug,
			Title:    e.Title,
			Type:     e.Type,
			Discount: e.Deal.Cut,
			Price:    e.Deal.Price.Amount,
			Regular:  e.Deal.Regular.Amount,
			Currency: e.Deal.Price.Currency,
			ShopName: e.Deal.Shop.Name,
			ShopID:   e.Deal.Shop.ID,
			URL:      e.Deal.URL,
			Flag:     e.Deal.Flag,
			IsHistLow: e.Deal.Flag == "H" || e.Deal.Flag == "N",
			DedupID:  fmt.Sprintf("itad-%s-%d-%d", e.ID, e.Deal.Shop.ID, e.Deal.Cut),
		}

		if ts, err := time.Parse(time.RFC3339, e.Deal.Timestamp); err == nil {
			deal.Timestamp = ts
		}
		if e.Deal.Expiry != nil {
			if exp, err := time.Parse(time.RFC3339, *e.Deal.Expiry); err == nil {
				deal.Expiry = &exp
			}
		}

		deals = append(deals, deal)
	}

	return deals, nil
}

// FilterITADDeals filters ITAD deals by discount and price thresholds.
func FilterITADDeals(deals []ITADDeal, minDiscount int, maxPrice float64) []ITADDeal {
	var filtered []ITADDeal
	for _, d := range deals {
		if d.Discount < minDiscount {
			slog.Debug("itad: skipping (low discount)", "title", d.Title, "discount", d.Discount)
			continue
		}
		if d.Price > maxPrice {
			slog.Debug("itad: skipping (too expensive)", "title", d.Title, "price", d.Price)
			continue
		}
		filtered = append(filtered, d)
	}
	return filtered
}

// LookupHistoricalLows checks if games are at their historical low price via ITAD.
// Takes a map of steamAppID -> deal index, returns map of steamAppID -> isHistoricalLow.
func LookupHistoricalLows(apiKey string, steamAppIDs []string) (map[string]bool, error) {
	result := make(map[string]bool)
	if apiKey == "" || len(steamAppIDs) == 0 {
		return result, nil
	}

	// Step 1: Look up ITAD game IDs from Steam app IDs
	itadIDs := make(map[string]string) // steamAppID -> itadID
	for _, appID := range steamAppIDs {
		if appID == "" || appID == "0" {
			continue
		}
		url := fmt.Sprintf(
			"https://api.isthereanydeal.com/games/lookup/v1?key=%s&appid=%s",
			apiKey, appID,
		)
		resp, err := http.Get(url)
		if err != nil {
			slog.Warn("itad lookup failed", "appid", appID, "error", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			slog.Warn("itad lookup returned non-200", "appid", appID, "status", resp.StatusCode)
			resp.Body.Close()
			continue
		}

		var lookup struct {
			Found bool `json:"found"`
			Game  struct {
				ID string `json:"id"`
			} `json:"game"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&lookup); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		if lookup.Found {
			itadIDs[appID] = lookup.Game.ID
		}
	}

	if len(itadIDs) == 0 {
		return result, nil
	}

	// Step 2: Get price overview for all looked-up games
	var gameIDs []string
	itadToSteam := make(map[string]string)
	for steamID, itadID := range itadIDs {
		gameIDs = append(gameIDs, itadID)
		itadToSteam[itadID] = steamID
	}

	body, err := json.Marshal(gameIDs)
	if err != nil {
		return result, fmt.Errorf("itad overview marshal failed: %w", err)
	}
	url := fmt.Sprintf("https://api.isthereanydeal.com/games/overview/v2?key=%s", apiKey)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return result, fmt.Errorf("itad overview request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("itad overview returned status %d", resp.StatusCode)
	}

	var overview struct {
		Prices []struct {
			ID      string `json:"id"`
			Current struct {
				Price struct {
					Amount float64 `json:"amount"`
				} `json:"price"`
			} `json:"current"`
			Lowest struct {
				Price struct {
					Amount float64 `json:"amount"`
				} `json:"price"`
			} `json:"lowest"`
		} `json:"prices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&overview); err != nil {
		return result, fmt.Errorf("itad overview decode failed: %w", err)
	}

	for _, p := range overview.Prices {
		if steamID, ok := itadToSteam[p.ID]; ok {
			result[steamID] = p.Current.Price.Amount <= p.Lowest.Price.Amount
		}
	}

	return result, nil
}
