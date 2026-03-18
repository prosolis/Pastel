package watchlist

import (
	"strings"
	"time"
	"unicode"

	"github.com/jmoiron/sqlx"
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

// Normalize lowercases, strips non-alphanumeric (keeping spaces), and collapses whitespace.
func Normalize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
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
	var entries []WatchEntry
	err := s.db.Select(&entries,
		`SELECT id, user_id, game_name, game_name_normalized, created_at, expires_at, expiry_warned
		 FROM watchlist WHERE user_id = ? AND expires_at > CURRENT_TIMESTAMP
		 ORDER BY created_at`,
		userID,
	)
	return entries, err
}

// FindMatchingUsers finds all users whose watchlist entries match the given deal title.
// Loads all active entries and checks normalized substring containment in Go.
func (s *Store) FindMatchingUsers(dealTitle string) ([]Match, error) {
	normalizedTitle := Normalize(dealTitle)

	var entries []WatchEntry
	err := s.db.Select(&entries,
		`SELECT user_id, game_name, game_name_normalized
		 FROM watchlist WHERE expires_at > CURRENT_TIMESTAMP`,
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
