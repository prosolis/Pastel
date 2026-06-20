package watchlist

import (
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/prosolis/Pastel/internal/normalize"
)

const watchDuration = 180 * 24 * time.Hour

// WatchEntry represents a single watchlist entry.
type WatchEntry struct {
	ID                 int64     `db:"id"`
	UserID             string    `db:"user_id"`
	GameName           string    `db:"game_name"`
	GameNameNormalized string    `db:"game_name_normalized"`
	CreatedAt          time.Time `db:"created_at"`
	ExpiresAt          time.Time `db:"expires_at"`
	ExpiryWarned       int       `db:"expiry_warned"`
}

// Match represents a user whose watchlist entry matched a deal.
type Match struct {
	UserID   string
	GameName string // original user-provided name for display
}

// Store handles watchlist database operations.
type Store struct {
	db *sqlx.DB
}

// NewStore creates a new watchlist store.
func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db}
}

// Normalize lowercases, strips non-alphanumeric (keeping spaces), and collapses
// whitespace. It delegates to normalize.Text so the watchlist matcher and the
// web deal search stay in lockstep.
func Normalize(s string) string {
	return normalize.Text(s)
}

// AddWatch adds a game to a user's watchlist. Returns false if already watched.
func (s *Store) AddWatch(userID, gameName string) (bool, error) {
	normalized := Normalize(gameName)
	if normalized == "" {
		return false, nil
	}
	expiresAt := time.Now().Add(watchDuration).UTC().Format(time.RFC3339)
	result, err := s.db.Exec(
		`INSERT OR IGNORE INTO watchlist (user_id, game_name, game_name_normalized, expires_at)
		 VALUES (?, ?, ?, ?)`,
		userID, gameName, normalized, expiresAt,
	)
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

// RemoveWatch removes a game from a user's watchlist. Returns false if not found.
func (s *Store) RemoveWatch(userID, gameName string) (bool, error) {
	normalized := Normalize(gameName)
	result, err := s.db.Exec(
		"DELETE FROM watchlist WHERE user_id = ? AND game_name_normalized = ?",
		userID, normalized,
	)
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

// RemoveWatchByID removes a watch by its row ID, scoped to the owning user so
// one user cannot delete another's entry. Returns false if not found.
func (s *Store) RemoveWatchByID(userID string, id int64) (bool, error) {
	result, err := s.db.Exec(
		"DELETE FROM watchlist WHERE id = ? AND user_id = ?",
		id, userID,
	)
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

// ExtendWatch resets the expiry to 180 days from now. Returns false if not found.
func (s *Store) ExtendWatch(userID, gameName string) (bool, error) {
	normalized := Normalize(gameName)
	expiresAt := time.Now().Add(watchDuration).UTC().Format(time.RFC3339)
	result, err := s.db.Exec(
		`UPDATE watchlist SET expires_at = ?, expiry_warned = 0
		 WHERE user_id = ? AND game_name_normalized = ?`,
		expiresAt, userID, normalized,
	)
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

// ListWatches returns all active watches for a user.
func (s *Store) ListWatches(userID string) ([]WatchEntry, error) {
	// expires_at is stored as RFC3339 (with a "T"), which does not order
	// correctly against SQLite's CURRENT_TIMESTAMP ("YYYY-MM-DD HH:MM:SS"), so
	// compare against a Go-formatted RFC3339 "now" instead.
	now := time.Now().UTC().Format(time.RFC3339)
	var entries []WatchEntry
	err := s.db.Select(&entries,
		`SELECT id, user_id, game_name, game_name_normalized, created_at, expires_at, expiry_warned
		 FROM watchlist WHERE user_id = ? AND expires_at > ?
		 ORDER BY created_at`,
		userID, now,
	)
	return entries, err
}

// FindMatchingUsers finds all users whose watchlist entries match the given deal title.
// Loads all active entries and checks normalized substring containment in Go.
func (s *Store) FindMatchingUsers(dealTitle string) ([]Match, error) {
	normalizedTitle := Normalize(dealTitle)

	// Compare against a Go-formatted RFC3339 "now" (see ListWatches): expires_at
	// is RFC3339 and does not order correctly against CURRENT_TIMESTAMP.
	now := time.Now().UTC().Format(time.RFC3339)
	var entries []WatchEntry
	err := s.db.Select(&entries,
		`SELECT user_id, game_name, game_name_normalized
		 FROM watchlist WHERE expires_at > ?`,
		now,
	)
	if err != nil {
		return nil, err
	}

	var matches []Match
	for _, e := range entries {
		if strings.Contains(normalizedTitle, e.GameNameNormalized) {
			matches = append(matches, Match{
				UserID:   e.UserID,
				GameName: e.GameName,
			})
		}
	}
	return matches, nil
}

// GetExpiringWatches returns entries expiring within the given number of days
// that haven't been warned yet.
func (s *Store) GetExpiringWatches(withinDays int) ([]WatchEntry, error) {
	deadline := time.Now().AddDate(0, 0, withinDays).UTC().Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	var entries []WatchEntry
	err := s.db.Select(&entries,
		`SELECT id, user_id, game_name, game_name_normalized, created_at, expires_at, expiry_warned
		 FROM watchlist
		 WHERE expires_at > ? AND expires_at <= ? AND expiry_warned = 0`,
		now, deadline,
	)
	return entries, err
}

// MarkExpiryWarned sets the expiry_warned flag on an entry.
func (s *Store) MarkExpiryWarned(id int64) error {
	_, err := s.db.Exec("UPDATE watchlist SET expiry_warned = 1 WHERE id = ?", id)
	return err
}

// PurgeExpired deletes entries past their expiry date. Returns count deleted.
func (s *Store) PurgeExpired() (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.Exec("DELETE FROM watchlist WHERE expires_at <= ?", now)
	if err != nil {
		return 0, err
	}
	rows, _ := result.RowsAffected()
	return int(rows), nil
}
