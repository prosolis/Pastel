package watchlist

import "time"

// NotifyMode reports a user's notification mode ("instant" or "daily"),
// defaulting to "instant" when the user has no stored preference.
func (s *Store) NotifyMode(userID string) (string, error) {
	var mode string
	err := s.db.Get(&mode, "SELECT notify_mode FROM user_prefs WHERE user_id = ?", userID)
	if err != nil {
		// No row → default. sqlx returns sql.ErrNoRows; treat any miss as default.
		return "instant", nil
	}
	if mode != "daily" {
		mode = "instant"
	}
	return mode, nil
}

// SetNotifyMode upserts a user's notification mode.
func (s *Store) SetNotifyMode(userID, mode string) error {
	if mode != "daily" {
		mode = "instant"
	}
	_, err := s.db.Exec(
		`INSERT INTO user_prefs (user_id, notify_mode, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id) DO UPDATE SET notify_mode = excluded.notify_mode, updated_at = CURRENT_TIMESTAMP`,
		userID, mode,
	)
	return err
}

// DigestItem is one queued match awaiting a daily digest.
type DigestItem struct {
	Label    string `db:"label"`
	Title    string `db:"title"`
	URL      string `db:"url"`
	Price    string `db:"price"`
	Discount int    `db:"discount"`
	IsFree   int    `db:"is_free"`
}

// QueueDigest appends a matched deal to a user's pending daily digest.
func (s *Store) QueueDigest(userID, label string, d MatchDeal, url, price string) error {
	free := 0
	if d.IsFree {
		free = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO pending_digest (user_id, label, title, url, price, discount, is_free)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, label, d.Title, url, price, d.Discount, free,
	)
	return err
}

// PendingDigestUsers lists the distinct users with queued digest items.
func (s *Store) PendingDigestUsers() ([]string, error) {
	var users []string
	err := s.db.Select(&users, "SELECT DISTINCT user_id FROM pending_digest ORDER BY user_id")
	return users, err
}

// TakeDigest returns and clears all queued items for a user, so a digest is sent
// exactly once. Selection and deletion run in one transaction.
func (s *Store) TakeDigest(userID string) ([]DigestItem, error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var items []DigestItem
	if err := tx.Select(&items,
		`SELECT label, title, url, price, discount, is_free
		 FROM pending_digest WHERE user_id = ? ORDER BY queued_at`,
		userID,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec("DELETE FROM pending_digest WHERE user_id = ?", userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

// RestoreDigest re-queues items previously removed by TakeDigest, used to
// recover a digest whose DM failed to send so the matches aren't lost — they
// simply wait for the next flush. Order is reset to "now", which is acceptable
// for a once-a-day summary.
func (s *Store) RestoreDigest(userID string, items []DigestItem) error {
	for _, it := range items {
		if _, err := s.db.Exec(
			`INSERT INTO pending_digest (user_id, label, title, url, price, discount, is_free)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			userID, it.Label, it.Title, it.URL, it.Price, it.Discount, it.IsFree,
		); err != nil {
			return err
		}
	}
	return nil
}

// PruneDigest drops digest items older than the given age, a safety valve so a
// user who never receives a flush (e.g. left the room) doesn't accumulate rows
// forever. Mirrors the other pruning paths.
func (s *Store) PruneDigest(maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge).UTC().Format(time.RFC3339)
	_, err := s.db.Exec("DELETE FROM pending_digest WHERE queued_at <= ?", cutoff)
	return err
}
