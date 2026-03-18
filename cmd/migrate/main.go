// Command migrate converts a Pastel Python database to the Go format.
//
// The Python bot writes posted_at timestamps using SQLite's CURRENT_TIMESTAMP
// ("YYYY-MM-DD HH:MM:SS") and prunes with Python's isoformat()
// ("YYYY-MM-DDTHH:MM:SS+00:00"). The Go bot uses RFC 3339 ("YYYY-MM-DDTHH:MM:SSZ").
//
// This script normalizes all posted_at values to RFC 3339 so the Go bot can use
// its native format consistently. It also creates any new tables (watchlist) that
// the Python version didn't have.
//
// Usage:
//
//	go run ./cmd/migrate [path-to-deals.db]
//
// The database is modified in place. A backup is created at <path>.bak first.
package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Formats the Python bot may have written.
var parseFormats = []string{
	"2006-01-02 15:04:05",         // SQLite CURRENT_TIMESTAMP
	"2006-01-02T15:04:05+00:00",   // Python datetime.isoformat()
	"2006-01-02T15:04:05Z",        // RFC 3339 (already correct)
	"2006-01-02T15:04:05-07:00",   // RFC 3339 with offset
}

func main() {
	dbPath := "deals.db"
	if len(os.Args) > 1 {
		dbPath = os.Args[1]
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Database not found: %s\n", dbPath)
		os.Exit(1)
	}

	// Create backup
	backupPath := dbPath + ".bak"
	if err := copyFile(dbPath, backupPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create backup: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Backup created: %s\n", backupPath)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Ensure new tables exist (watchlist, etc.)
	if _, err := db.Exec(`
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
	`); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create new tables: %v\n", err)
		os.Exit(1)
	}

	// Convert posted_at timestamps
	rows, err := db.Query("SELECT id, posted_at FROM posted_deals")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to query deals: %v\n", err)
		os.Exit(1)
	}

	type update struct {
		id        string
		converted string
	}
	var updates []update

	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			continue
		}

		converted, changed := normalizeTimestamp(raw)
		if changed {
			updates = append(updates, update{id, converted})
		}
	}
	rows.Close()

	if len(updates) == 0 {
		fmt.Println("No timestamps need conversion. Database is already in Go format.")
		return
	}

	tx, err := db.Begin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to begin transaction: %v\n", err)
		os.Exit(1)
	}

	stmt, err := tx.Prepare("UPDATE posted_deals SET posted_at = ? WHERE id = ?")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to prepare statement: %v\n", err)
		os.Exit(1)
	}

	for _, u := range updates {
		if _, err := stmt.Exec(u.converted, u.id); err != nil {
			tx.Rollback()
			fmt.Fprintf(os.Stderr, "Failed to update %s: %v\n", u.id, err)
			os.Exit(1)
		}
	}
	stmt.Close()

	if err := tx.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to commit: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Converted %d timestamps to RFC 3339.\n", len(updates))
	fmt.Println("Migration complete.")
}

func normalizeTimestamp(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)

	for _, layout := range parseFormats {
		if t, err := time.Parse(layout, raw); err == nil {
			rfc := t.UTC().Format(time.RFC3339)
			return rfc, rfc != raw
		}
	}

	// Unparseable — leave as-is
	return raw, false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
