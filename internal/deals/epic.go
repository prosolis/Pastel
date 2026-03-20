package deals

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type EpicFreeGame struct {
	ID       string
	Title    string
	Desc     string
	URL      string
	EndDate  *time.Time
	Upcoming bool
	DedupID  string
}

type epicResponse struct {
	Data struct {
		Catalog struct {
			SearchStore struct {
				Elements []epicElement `json:"elements"`
			} `json:"searchStore"`
		} `json:"Catalog"`
	} `json:"data"`
}

type epicElement struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ProductSlug string `json:"productSlug"`
	URLSlug     string `json:"urlSlug"`
	CatalogNs   struct {
		Mappings []struct {
			PageSlug string `json:"pageSlug"`
		} `json:"mappings"`
	} `json:"catalogNs"`
	Promotions *struct {
		PromotionalOffers         []epicOfferGroup `json:"promotionalOffers"`
		UpcomingPromotionalOffers []epicOfferGroup `json:"upcomingPromotionalOffers"`
	} `json:"promotions"`
}

type epicOfferGroup struct {
	PromotionalOffers []epicOffer `json:"promotionalOffers"`
}

type epicOffer struct {
	StartDate       string `json:"startDate"`
	EndDate         string `json:"endDate"`
	DiscountSetting struct {
		DiscountPercentage int `json:"discountPercentage"`
	} `json:"discountSetting"`
}

// FetchEpicFreeGames fetches current and upcoming free games from Epic Games Store.
func FetchEpicFreeGames() ([]EpicFreeGame, error) {
	resp, err := http.Get("https://store-site-backend-static.ak.epicgames.com/freeGamesPromotions?locale=en-US")
	if err != nil {
		return nil, fmt.Errorf("epic request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("epic returned status %d", resp.StatusCode)
	}

	var result epicResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("epic decode failed: %w", err)
	}

	now := time.Now()
	var games []EpicFreeGame

	for _, elem := range result.Data.Catalog.SearchStore.Elements {
		if elem.Promotions == nil {
			continue
		}

		slug := elem.ProductSlug
		if slug == "" {
			slug = elem.URLSlug
		}
		if slug == "" && len(elem.CatalogNs.Mappings) > 0 {
			slug = elem.CatalogNs.Mappings[0].PageSlug
		}
		if slug == "" {
			slog.Warn("epic: skipping game with no slug", "title", elem.Title, "id", elem.ID)
			continue
		}
		storeURL := fmt.Sprintf("https://store.epicgames.com/en-US/p/%s", slug)

		// Check current free offers
		for _, group := range elem.Promotions.PromotionalOffers {
			for _, offer := range group.PromotionalOffers {
				if offer.DiscountSetting.DiscountPercentage != 0 {
					continue
				}
				start, errS := time.Parse(time.RFC3339, offer.StartDate)
				end, errE := time.Parse(time.RFC3339, offer.EndDate)
				if errS != nil || errE != nil {
					slog.Warn("epic: failed to parse offer dates", "title", elem.Title, "start", offer.StartDate, "end", offer.EndDate)
					continue
				}

				if start.After(now) || end.Before(now) {
					continue
				}

				game := EpicFreeGame{
					ID:       elem.ID,
					Title:    elem.Title,
					Desc:     elem.Description,
					URL:      storeURL,
					Upcoming: false,
					DedupID:  fmt.Sprintf("epic-current-%s", elem.ID),
				}
				if !end.IsZero() {
					game.EndDate = &end
				}
				games = append(games, game)
			}
		}

		// Check upcoming free offers
		for _, group := range elem.Promotions.UpcomingPromotionalOffers {
			for _, offer := range group.PromotionalOffers {
				if offer.DiscountSetting.DiscountPercentage != 0 {
					continue
				}

				game := EpicFreeGame{
					ID:       elem.ID,
					Title:    elem.Title,
					Desc:     elem.Description,
					URL:      storeURL,
					Upcoming: true,
					DedupID:  fmt.Sprintf("epic-upcoming-%s", elem.ID),
				}
				if offer.EndDate != "" {
					if end, err := time.Parse(time.RFC3339, offer.EndDate); err == nil {
						game.EndDate = &end
					}
				}
				games = append(games, game)
			}
		}
	}

	return games, nil
}
