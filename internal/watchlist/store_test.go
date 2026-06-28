package watchlist

import (
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore builds an in-memory store with the Phase 2 watchlist schema.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.MustExec(`
		CREATE TABLE watchlist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			game_name TEXT NOT NULL,
			game_name_normalized TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL,
			expiry_warned INTEGER DEFAULT 0,
			max_price REAL NOT NULL DEFAULT 0,
			min_discount INTEGER NOT NULL DEFAULT 0,
			category TEXT NOT NULL DEFAULT '',
			UNIQUE(user_id, game_name_normalized)
		);
		CREATE TABLE user_prefs (
			user_id TEXT PRIMARY KEY,
			notify_mode TEXT NOT NULL DEFAULT 'instant',
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE pending_digest (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			label TEXT NOT NULL,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			price TEXT,
			discount INTEGER DEFAULT 0,
			is_free INTEGER DEFAULT 0,
			queued_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return NewStore(db)
}

func mustAdd(t *testing.T, s *Store, user, args string) bool {
	t.Helper()
	added, err := s.AddWatch(user, ParseWatch(args))
	if err != nil {
		t.Fatalf("AddWatch(%q): %v", args, err)
	}
	return added
}

func TestAddWatchUpsert(t *testing.T) {
	s := newTestStore(t)
	if !mustAdd(t, s, "@u", "elden ring under 40") {
		t.Fatal("first add should report new")
	}
	// Re-watching the same label refines (returns not-new) and updates predicates.
	if mustAdd(t, s, "@u", "elden ring under 30") {
		t.Fatal("second add should report existing/updated")
	}
	entries, err := s.ListWatches("@u")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].MaxPrice != 30 {
		t.Fatalf("MaxPrice = %v, want 30 (refined)", entries[0].MaxPrice)
	}
}

func TestFindMatchingUsersPredicates(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, "@cheap", "elden ring under 30")
	mustAdd(t, s, "@disc", "laptop over 40% off")
	mustAdd(t, s, "@cat", "category:clothing nike")
	mustAdd(t, s, "@plain", "elden ring")

	tests := []struct {
		name  string
		deal  MatchDeal
		users []string
	}{
		{
			"under cap passes",
			MatchDeal{Title: "Elden Ring", Category: "games", PriceUSD: 25, Discount: 50},
			[]string{"@cheap", "@plain"},
		},
		{
			"over cap fails predicate watch only",
			MatchDeal{Title: "Elden Ring", Category: "games", PriceUSD: 45, Discount: 10},
			[]string{"@plain"},
		},
		{
			"discount threshold met",
			MatchDeal{Title: "Gaming Laptop", Category: "tech", PriceUSD: 800, Discount: 45},
			[]string{"@disc"},
		},
		{
			"discount threshold not met",
			MatchDeal{Title: "Gaming Laptop", Category: "tech", PriceUSD: 800, Discount: 20},
			nil,
		},
		{
			"category mismatch excludes",
			MatchDeal{Title: "Nike Air", Category: "tech", PriceUSD: 100},
			nil,
		},
		{
			"category match includes",
			MatchDeal{Title: "Nike Air Max", Category: "clothing", PriceUSD: 100},
			[]string{"@cat"},
		},
		{
			"free deal satisfies any price cap",
			MatchDeal{Title: "Elden Ring", Category: "games", IsFree: true},
			[]string{"@cheap", "@plain"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches, err := s.FindMatchingUsers(tt.deal)
			if err != nil {
				t.Fatal(err)
			}
			got := make(map[string]bool)
			for _, m := range matches {
				got[m.UserID] = true
			}
			if len(got) != len(tt.users) {
				t.Fatalf("matched %v, want %v", keys(got), tt.users)
			}
			for _, u := range tt.users {
				if !got[u] {
					t.Fatalf("missing %s; matched %v", u, keys(got))
				}
			}
		})
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestDigestQueueAndTake(t *testing.T) {
	s := newTestStore(t)

	if mode, _ := s.NotifyMode("@u"); mode != "instant" {
		t.Fatalf("default mode = %q, want instant", mode)
	}
	if err := s.SetNotifyMode("@u", "daily"); err != nil {
		t.Fatal(err)
	}
	if mode, _ := s.NotifyMode("@u"); mode != "daily" {
		t.Fatalf("mode after set = %q, want daily", mode)
	}

	d := MatchDeal{Title: "Elden Ring", Discount: 50}
	if err := s.QueueDigest("@u", "elden ring", d, "http://x", "$25"); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueDigest("@u", "elden ring", MatchDeal{Title: "DLC", IsFree: true}, "http://y", "Free"); err != nil {
		t.Fatal(err)
	}

	users, err := s.PendingDigestUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0] != "@u" {
		t.Fatalf("PendingDigestUsers = %v, want [@u]", users)
	}

	items, err := s.TakeDigest("@u")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("took %d items, want 2", len(items))
	}
	// Taking clears the queue.
	again, err := s.TakeDigest("@u")
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second take returned %d, want 0", len(again))
	}
}
