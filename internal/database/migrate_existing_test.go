package database

import (
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// TestMigrateAddsCategoryToExistingDB simulates a pre-category production
// database: create the old deals schema with a row, then Open() (which runs
// migrate) and confirm the category column is added and backfilled to 'games'.
func TestMigrateAddsCategoryToExistingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sqlx.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Old schema: no category column, plus the source index (as prod has).
	old.MustExec(`CREATE TABLE deals (
		dedup_id TEXT PRIMARY KEY, source TEXT NOT NULL, kind TEXT NOT NULL,
		title TEXT NOT NULL, title_normalized TEXT NOT NULL, store TEXT,
		sale_price REAL, normal_price REAL, discount INTEGER, rating REAL, url TEXT,
		is_hist_low INTEGER DEFAULT 0, is_free INTEGER DEFAULT 0, upcoming INTEGER DEFAULT 0,
		expires_at TIMESTAMP, posted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`)
	old.MustExec(`INSERT INTO deals (dedup_id, source, kind, title, title_normalized) VALUES ('x','cheapshark','game','Celeste','celeste')`)
	old.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open/migrate on existing DB failed: %v", err)
	}
	defer db.Close()

	var cat string
	if err := db.RawDB().Get(&cat, "SELECT category FROM deals WHERE dedup_id='x'"); err != nil {
		t.Fatalf("category column missing after migrate: %v", err)
	}
	if cat != "games" {
		t.Fatalf("existing row backfilled to %q, want games", cat)
	}

	// Phase 3 columns/tables reach the pre-existing deals table too: a reaction
	// recorded against a backfilled deal must increment its reaction_count, which
	// exercises event_id, reaction_count, and the deal_reactions table together.
	if err := db.SetDealEventID("x", "$evt-x"); err != nil {
		t.Fatalf("set event id after migrate: %v", err)
	}
	if isDeal, err := db.AddReaction("$evt-x", "@u:x", "$r-x"); err != nil || !isDeal {
		t.Fatalf("add reaction after migrate: isDeal=%v err=%v", isDeal, err)
	}
	var reacts int
	if err := db.RawDB().Get(&reacts, "SELECT reaction_count FROM deals WHERE dedup_id='x'"); err != nil {
		t.Fatalf("reaction_count column missing after migrate: %v", err)
	}
	if reacts != 1 {
		t.Fatalf("reaction_count = %d after one reaction, want 1", reacts)
	}

	// Idempotent: a second Open must not error.
	db.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (idempotency) failed: %v", err)
	}
	db2.Close()
}
