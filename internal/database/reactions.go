package database

import (
	"database/sql"
	"time"
)

// SetDealEventID links a posted deal (by dedup_id) to the Matrix event ID of the
// message it was posted as, so reactions targeting that event can be attributed
// back to the deal. Idempotent and safe to call on re-posts; SaveDeal's upsert
// never touches event_id, so the mapping survives later price refreshes.
func (d *DB) SetDealEventID(dedupID, eventID string) error {
	if dedupID == "" || eventID == "" {
		return nil
	}
	_, err := d.db.Exec("UPDATE deals SET event_id = ? WHERE dedup_id = ?", eventID, dedupID)
	return err
}

// dealEventExists reports whether eventID is the message event of a known deal.
func (d *DB) dealEventExists(eventID string) (bool, error) {
	if eventID == "" {
		return false, nil
	}
	var n int
	if err := d.db.Get(&n, "SELECT COUNT(1) FROM deals WHERE event_id = ?", eventID); err != nil {
		return false, err
	}
	return n > 0, nil
}

// AddReaction records that userID reacted (via the reaction event reactionEventID)
// to the deal message targetEventID, then refreshes that deal's reaction_count to
// the number of distinct users who have reacted. It is idempotent: re-delivering
// the same reaction event — e.g. when a restart replays recent timeline — inserts
// nothing new and leaves the count unchanged. Reactions to events that are not
// deal messages are ignored. The bool reports whether the target was a deal.
func (d *DB) AddReaction(targetEventID, userID, reactionEventID string) (bool, error) {
	if targetEventID == "" || reactionEventID == "" || userID == "" {
		return false, nil
	}
	isDeal, err := d.dealEventExists(targetEventID)
	if err != nil || !isDeal {
		return false, err
	}
	if _, err := d.db.Exec(
		"INSERT OR IGNORE INTO deal_reactions (reaction_event_id, target_event_id, user_id) VALUES (?, ?, ?)",
		reactionEventID, targetEventID, userID,
	); err != nil {
		return true, err
	}
	return true, d.refreshReactionCount(targetEventID)
}

// RemoveReaction removes a previously-recorded reaction by its own event ID — the
// path taken when a member un-reacts (the homeserver redacts the reaction event)
// — and refreshes the affected deal's reaction_count. A reaction we never tracked
// (redaction of some other event) is a no-op.
func (d *DB) RemoveReaction(reactionEventID string) error {
	if reactionEventID == "" {
		return nil
	}
	var target string
	err := d.db.Get(&target, "SELECT target_event_id FROM deal_reactions WHERE reaction_event_id = ?", reactionEventID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := d.db.Exec("DELETE FROM deal_reactions WHERE reaction_event_id = ?", reactionEventID); err != nil {
		return err
	}
	return d.refreshReactionCount(target)
}

// refreshReactionCount recomputes deals.reaction_count for the deal whose message
// is targetEventID as the number of DISTINCT users who reacted, so multiple emoji
// from one member count once and a redaction decrements correctly.
func (d *DB) refreshReactionCount(targetEventID string) error {
	_, err := d.db.Exec(
		`UPDATE deals SET reaction_count =
			(SELECT COUNT(DISTINCT user_id) FROM deal_reactions WHERE target_event_id = ?)
		 WHERE event_id = ?`,
		targetEventID, targetEventID,
	)
	return err
}

// TopDealsSince returns up to limit deals posted within the last `days` days that
// have received at least one reaction, ranked by reaction_count then recency. It
// backs the weekly "Top 5 this week" room digest. watcher_count is not selected
// here (it stays zero), as the digest doesn't surface it.
func (d *DB) TopDealsSince(days, limit int) ([]Deal, error) {
	cutoff := time.Now().AddDate(0, 0, -days).UTC().Format("2006-01-02 15:04:05")
	query := "SELECT " + dealColumns + " FROM deals " +
		"WHERE reaction_count > 0 AND posted_at >= ? " +
		"ORDER BY reaction_count DESC, posted_at DESC LIMIT ?"
	var deals []Deal
	if err := d.db.Select(&deals, query, cutoff, limit); err != nil {
		return nil, err
	}
	return deals, nil
}

// PruneReactions removes reaction rows whose deal message is no longer present
// (the deal aged out of the deals table). Called alongside the other pruners so
// deal_reactions stays bounded.
func (d *DB) PruneReactions() error {
	_, err := d.db.Exec(
		"DELETE FROM deal_reactions WHERE target_event_id NOT IN (SELECT event_id FROM deals WHERE event_id != '')",
	)
	return err
}
