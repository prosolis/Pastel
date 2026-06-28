package main

import (
	"log/slog"
	"math"

	"github.com/prosolis/Pastel/internal/database"
	"github.com/prosolis/Pastel/internal/deals"
	"github.com/prosolis/Pastel/internal/watchlist"
)

// saveCheapSharkDeals records the full data of the given (already filtered)
// CheapShark deals so the web interface can browse them. Unlike posting, this
// runs for every current deal, not just newly-posted ones, so the gallery
// always reflects what is on offer right now.
func saveCheapSharkDeals(db *database.DB, filtered []deals.CheapSharkDeal) {
	for _, d := range filtered {
		deal := database.Deal{
			DedupID:     d.DedupID,
			Source:      "cheapshark",
			Category:    "games",
			Kind:        "game",
			Title:       d.Title,
			TitleNorm:   watchlist.Normalize(d.Title),
			Store:       d.StoreName,
			SalePrice:   d.SalePrice,
			NormalPrice: d.NormalPrice,
			Discount:    int(math.Floor(d.Savings)),
			Rating:      d.DealRating,
			URL:         d.DealURL,
			ImageURL:    d.ImageURL,
			IsHistLow:   database.Bool(d.IsHistLow),
			IsFree:      database.Bool(d.SalePrice == 0),
		}
		if err := db.SaveDealWithVerdict(deal); err != nil {
			slog.Warn("failed to save cheapshark deal for web", "title", d.Title, "error", err)
		}
	}
}

// saveITADDeals records the full data of the given (already filtered) ITAD deals.
func saveITADDeals(db *database.DB, filtered []deals.ITADDeal) {
	for _, d := range filtered {
		kind := "game"
		if d.Type == "dlc" || d.Type == "DLC" {
			kind = "dlc"
		}
		deal := database.Deal{
			DedupID:     d.DedupID,
			Source:      "itad",
			Category:    "games",
			Kind:        kind,
			Title:       d.Title,
			TitleNorm:   watchlist.Normalize(d.Title),
			Store:       d.ShopName,
			SalePrice:   d.Price,
			NormalPrice: d.Regular,
			Discount:    d.Discount,
			URL:         d.URL,
			IsHistLow:   database.Bool(d.IsHistLow),
			IsFree:      database.Bool(d.Price == 0),
			ExpiresAt:   d.Expiry,
		}
		if err := db.SaveDealWithVerdict(deal); err != nil {
			slog.Warn("failed to save itad deal for web", "title", d.Title, "error", err)
		}
	}
}

// saveWebDeals records deals scraped from RSS aggregators (DealNews, Slickdeals,
// …). These populate the web gallery's non-game categories (tech, clothing,
// home, …); they are intentionally not posted to Matrix, which stays focused on
// game deals.
func saveWebDeals(db *database.DB, items []deals.WebDeal) {
	for _, d := range items {
		deal := database.Deal{
			DedupID:   d.DedupID,
			Source:    d.Source,
			Category:  d.Category,
			Kind:      "deal",
			Title:     d.Title,
			TitleNorm: watchlist.Normalize(d.Title),
			Store:     d.Store,
			SalePrice: d.Price,
			Discount:  d.Discount,
			URL:       d.URL,
			ImageURL:  d.ImageURL,
			IsFree:    database.Bool(d.IsFree),
		}
		if err := db.SaveDealWithVerdict(deal); err != nil {
			slog.Warn("failed to save web deal", "source", d.Source, "title", d.Title, "error", err)
		}
	}
}

// saveEpicFreeGames records the full data of the given Epic free games.
func saveEpicFreeGames(db *database.DB, games []deals.EpicFreeGame) {
	for _, g := range games {
		deal := database.Deal{
			DedupID:   g.DedupID,
			Source:    "epic",
			Category:  "games",
			Kind:      "free",
			Title:     g.Title,
			TitleNorm: watchlist.Normalize(g.Title),
			Store:     "Epic Games",
			Discount:  100,
			URL:       g.URL,
			ImageURL:  g.ImageURL,
			IsFree:    true,
			Upcoming:  database.Bool(g.Upcoming),
			ExpiresAt: g.EndDate,
		}
		if err := db.SaveDealWithVerdict(deal); err != nil {
			slog.Warn("failed to save epic game for web", "title", g.Title, "error", err)
		}
	}
}
