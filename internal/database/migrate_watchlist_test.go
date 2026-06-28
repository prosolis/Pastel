package database

import (
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// TestMigrateAddsWatchlistPredicateColumns simulates a pre-Phase-2 production
// database: an existing watchlist table without the predicate/category columns
// and no user_prefs/pending_digest tables. Open() (which runs migrate) must add
// the columns to the existing row and create the new tables.
func TestMigrateAddsWatchlistPredicateColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sqlx.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old.MustExec(`CREATE TABLE watchlist (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		game_name TEXT NOT NULL,
		game_name_normalized TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMP NOT NULL,
		expiry_warned INTEGER DEFAULT 0,
		UNIQUE(user_id, game_name_normalized))`)
	old.MustExec(`INSERT INTO watchlist (user_id, game_name, game_name_normalized, expires_at)
		VALUES ('@u','Elden Ring','elden ring','2099-01-01T00:00:00Z')`)
	old.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open/migrate on existing DB failed: %v", err)
	}
	defer db.Close()

	// New columns exist and default sanely on the existing row.
	var row struct {
		MaxPrice    float64 `db:"max_price"`
		MinDiscount int     `db:"min_discount"`
		Category    string  `db:"category"`
	}
	if err := db.RawDB().Get(&row,
		"SELECT max_price, min_discount, category FROM watchlist WHERE user_id='@u'"); err != nil {
		t.Fatalf("predicate columns missing after migrate: %v", err)
	}
	if row.MaxPrice != 0 || row.MinDiscount != 0 || row.Category != "" {
		t.Fatalf("existing row got non-default predicates: %+v", row)
	}

	// New tables exist.
	for _, tbl := range []string{"user_prefs", "pending_digest"} {
		var n int
		if err := db.RawDB().Get(&n,
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl); err != nil {
			t.Fatalf("query for %s failed: %v", tbl, err)
		}
		if n != 1 {
			t.Fatalf("table %s missing after migrate", tbl)
		}
	}
}
