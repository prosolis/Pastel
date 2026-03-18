package database

import (
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

type DB struct {
	db *sqlx.DB
}

func Open(path string) (*DB, error) {
	db, err := sqlx.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	d := &DB{db: db}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

// RawDB exposes the underlying sqlx.DB for packages that need direct access.
func (d *DB) RawDB() *sqlx.DB {
	return d.db
}

func (d *DB) migrate() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS posted_deals (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			title TEXT NOT NULL,
			posted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT
		);
		CREATE TABLE IF NOT EXISTS watchlist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			game_name TEXT NOT NULL,
			game_name_normalized TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL,
			expiry_warned INTEGER DEFAULT 0,
			UNIQUE(user_id, game_name_normalized)
		);
		CREATE INDEX IF NOT EXISTS idx_watchlist_normalized ON watchlist(game_name_normalized);
		CREATE INDEX IF NOT EXISTS idx_watchlist_expires ON watchlist(expires_at);
	`)
	return err
}

// IsPosted checks if a deal has already been posted.
func (d *DB) IsPosted(id string) (bool, error) {
	var count int
	err := d.db.Get(&count, "SELECT COUNT(1) FROM posted_deals WHERE id = ?", id)
	return count > 0, err
}

// MarkPosted records a deal as posted.
func (d *DB) MarkPosted(id, source, title string) error {
	_, err := d.db.Exec(
		"INSERT OR IGNORE INTO posted_deals (id, source, title) VALUES (?, ?, ?)",
		id, source, title,
	)
	return err
}

// IsFirstRunDone checks if the initial population has been completed.
func (d *DB) IsFirstRunDone() (bool, error) {
	var val string
	err := d.db.Get(&val, "SELECT value FROM config WHERE key = 'first_run_done'")
	if err != nil {
		return false, nil // not found = not done
	}
	return val == "true", nil
}

// SetFirstRunDone marks the initial population as complete.
func (d *DB) SetFirstRunDone() error {
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO config (key, value) VALUES ('first_run_done', 'true')",
	)
	return err
}

// PruneOldDeals removes deals older than the given number of days.
func (d *DB) PruneOldDeals(days int) error {
	cutoff := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	_, err := d.db.Exec("DELETE FROM posted_deals WHERE posted_at < ?", cutoff)
	return err
}
