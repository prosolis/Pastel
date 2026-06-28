package database

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndQueryDeals(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	exp := time.Now().Add(48 * time.Hour).UTC()
	deals := []Deal{
		{
			DedupID: "cs-1", Source: "cheapshark", Kind: "game", Title: "Hollow Knight",
			TitleNorm: "hollow knight", Store: "Steam", SalePrice: 7.49, NormalPrice: 14.99,
			Discount: 50, Rating: 9.2, URL: "https://x/1", IsHistLow: true,
		},
		{
			DedupID: "epic-1", Source: "epic", Kind: "free", Title: "Free Game",
			TitleNorm: "free game", Store: "Epic Games", Discount: 100,
			URL: "https://x/2", IsFree: true, ExpiresAt: &exp,
		},
	}
	for _, d := range deals {
		if err := db.SaveDeal(d); err != nil {
			t.Fatalf("save %s: %v", d.DedupID, err)
		}
	}

	// Upsert should not duplicate and should update mutable fields.
	updated := deals[0]
	updated.SalePrice = 3.74
	updated.Discount = 75
	if err := db.SaveDeal(updated); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	all, total, err := db.QueryDeals(DealFilter{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 2 || len(all) != 2 {
		t.Fatalf("expected 2 deals, got total=%d len=%d", total, len(all))
	}

	// Bool round-trip + upsert applied.
	byID := map[string]Deal{}
	for _, d := range all {
		byID[d.DedupID] = d
	}
	if !byID["cs-1"].IsHistLow {
		t.Errorf("cs-1 IsHistLow should be true")
	}
	if byID["cs-1"].Discount != 75 || byID["cs-1"].SalePrice != 3.74 {
		t.Errorf("upsert not applied: %+v", byID["cs-1"])
	}
	if !byID["epic-1"].IsFree || byID["epic-1"].ExpiresAt == nil {
		t.Errorf("epic-1 flags/expiry wrong: %+v", byID["epic-1"])
	}

	// Filters: free only.
	free, total, err := db.QueryDeals(DealFilter{FreeOnly: true})
	if err != nil || total != 1 || len(free) != 1 || free[0].DedupID != "epic-1" {
		t.Fatalf("free filter: total=%d err=%v deals=%+v", total, err, free)
	}

	// Filters: search + min discount.
	hk, total, err := db.QueryDeals(DealFilter{Query: "Hollow", MinDiscount: 60})
	if err != nil || total != 1 || hk[0].DedupID != "cs-1" {
		t.Fatalf("search filter: total=%d err=%v deals=%+v", total, err, hk)
	}

	// Facets. Seeded deals don't set a category, so SaveDeal defaults them all
	// to "games" — one distinct category.
	categories, sources, stores, err := db.DealFacets()
	if err != nil || len(categories) != 1 || len(sources) != 2 || len(stores) != 2 {
		t.Fatalf("facets: categories=%v sources=%v stores=%v err=%v", categories, sources, stores, err)
	}
}

// TestQueryDealsToleratesNullReals guards against the prod regression where
// rows from non-games sources (RSS) leave the nullable REAL columns NULL,
// causing "converting NULL to float64 is unsupported" during scan. The columns
// are coalesced to 0 in dealColumns, so QueryDeals must succeed and zero them.
func TestQueryDealsToleratesNullReals(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Insert a deal the way an RSS source would: no price history and no
	// numeric price/rating, leaving those nullable REAL columns NULL.
	db.db.MustExec(`INSERT INTO deals (dedup_id, source, category, kind, title, title_normalized, store)
		VALUES ('rss-1', 'dealnews', 'tech', 'deal', 'USB Cable', 'usb cable', 'Amazon')`)

	deals, total, err := db.QueryDeals(DealFilter{Categories: []string{"tech"}})
	if err != nil {
		t.Fatalf("query with NULL reals failed: %v", err)
	}
	if total != 1 || len(deals) != 1 {
		t.Fatalf("expected 1 deal, got total=%d len=%d", total, len(deals))
	}
	if d := deals[0]; d.SalePrice != 0 || d.NormalPrice != 0 || d.Rating != 0 || d.PriceLow != 0 {
		t.Errorf("NULL reals should coalesce to 0, got %+v", d)
	}
}
