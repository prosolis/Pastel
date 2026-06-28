package database

import (
	"path/filepath"
	"testing"

	"github.com/prosolis/Pastel/internal/normalize"
)

func TestComputeVerdict(t *testing.T) {
	tests := []struct {
		name        string
		salePrice   float64
		low         float64
		haveLow     bool
		priorObs    int
		itadHistLow bool
		want        string
	}{
		{"itad hist low wins regardless", 50, 10, true, 0, true, VerdictAllTimeLow},
		{"no history yet", 20, 0, false, 0, false, VerdictNone},
		{"zero price no claim", 0, 10, true, 5, false, VerdictNone},
		{"equal to record low is not atl", 10, 10, true, 5, false, VerdictGood},
		{"new low with enough history is atl", 8, 10, true, minObsForATL, false, VerdictAllTimeLow},
		{"new low but too little history is good", 8, 10, true, minObsForATL - 1, false, VerdictGood},
		{"within 10pct is good", 10.9, 10, true, 5, false, VerdictGood},
		{"at 10pct boundary is good", 11, 10, true, 5, false, VerdictGood},
		{"above 10pct is meh", 12, 10, true, 5, false, VerdictMeh},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeVerdict(tc.salePrice, tc.low, tc.haveLow, tc.priorObs, tc.itadHistLow)
			if got != tc.want {
				t.Errorf("ComputeVerdict(%v,%v,%v,%v,%v) = %q, want %q",
					tc.salePrice, tc.low, tc.haveLow, tc.priorObs, tc.itadHistLow, got, tc.want)
			}
		})
	}
}

func TestIsSuspectDiscount(t *testing.T) {
	tests := []struct {
		name        string
		category    string
		discount    int
		normalPrice float64
		median      float64
		haveMedian  bool
		want        bool
	}{
		{"games never suspect", "games", 95, 100, 0, false, false},
		{"modest non-game discount ok", "tech", 30, 100, 90, true, false},
		{"huge discount no history", "tech", 80, 100, 0, false, true},
		{"inflated msrp vs median", "clothing", 40, 400, 100, true, true},
		{"normal msrp not inflated", "clothing", 40, 250, 100, true, false},
		{"boundary 70pct is suspect", "home", 70, 100, 0, false, true},
		{"just under 70pct ok", "home", 69, 100, 0, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsSuspectDiscount(tc.category, tc.discount, tc.normalPrice, tc.median, tc.haveMedian)
			if got != tc.want {
				t.Errorf("IsSuspectDiscount(%q,%d,%v,%v,%v) = %v, want %v",
					tc.category, tc.discount, tc.normalPrice, tc.median, tc.haveMedian, got, tc.want)
			}
		})
	}
}

// TestSaveDealWithVerdict_PriceJourney drives the same product through the save
// path at varying prices and asserts the trust verdict tracks the accumulated
// history: first sighting makes no claim, then meh → good → all-time-low.
func TestSaveDealWithVerdict_PriceJourney(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	const title = "Mega Widget 9000"
	// One stable dedup_id so the deals row is upserted in place (its verdict
	// reflects the latest save), while price_history accumulates every sighting.
	mk := func(price float64) Deal {
		return Deal{
			DedupID:   "tech-widget-stable",
			Source:    "dealnews",
			Category:  "tech",
			Kind:      "deal",
			Title:     title,
			TitleNorm: normalize.Text(title),
			Store:     "SomeShop",
			SalePrice: price,
			Discount:  20,
		}
	}

	steps := []struct {
		price       float64
		wantVerdict string
	}{
		{30, VerdictNone}, // first sighting: no history → no claim
		{35, VerdictMeh},  // pricier than the 30 we saw → meh
		{32, VerdictGood}, // within 10% of 30, but only 2 distinct priors → good
		// 4th sighting: strictly below the 30 low AND 3 distinct priors
		// (30/35/32) clear minObsForATL → all-time-low.
		{28, VerdictAllTimeLow},
	}

	for _, s := range steps {
		if err := db.SaveDealWithVerdict(mk(s.price)); err != nil {
			t.Fatalf("save at %.0f: %v", s.price, err)
		}
		got := latestDeal(t, db, title)
		if got.Verdict != s.wantVerdict {
			t.Errorf("price %.0f: verdict = %q, want %q", s.price, got.Verdict, s.wantVerdict)
		}
	}

	if low, ok := db.LowestPrice(PriceKey("tech", "SomeShop", normalize.Text(title))); !ok || low != 28 {
		t.Errorf("LowestPrice = %v (ok=%v), want 28", low, ok)
	}
}

func latestDeal(t *testing.T, db *DB, title string) Deal {
	t.Helper()
	deals, _, err := db.QueryDeals(DealFilter{Query: title, Sort: "newest", Limit: 50})
	if err != nil {
		t.Fatalf("query deals: %v", err)
	}
	for _, d := range deals {
		if d.Title == title {
			return d
		}
	}
	t.Fatalf("deal %q not found", title)
	return Deal{}
}
