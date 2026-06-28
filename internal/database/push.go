package database

// Web Push subscription storage (Phase 5). A subscription is a browser endpoint
// plus its encryption keys, owned by a user (the Matrix mxid). The webpush
// package owns the crypto; this layer only persists the rows, so database stays
// free of any push dependency.

// PushSub is a stored browser push subscription.
type PushSub struct {
	Endpoint string `db:"endpoint"`
	UserID   string `db:"user_id"`
	P256dh   string `db:"p256dh"`
	Auth     string `db:"auth"`
}

// SavePushSub upserts a subscription. endpoint is the primary key, so a browser
// re-subscribing (new keys, or a different user on the same endpoint) overwrites
// the prior row rather than duplicating it.
func (d *DB) SavePushSub(userID, endpoint, p256dh, auth string) error {
	_, err := d.db.Exec(`
		INSERT INTO push_subscriptions (endpoint, user_id, p256dh, auth)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(endpoint) DO UPDATE SET
			user_id = excluded.user_id,
			p256dh  = excluded.p256dh,
			auth    = excluded.auth`,
		endpoint, userID, p256dh, auth)
	return err
}

// ListPushSubs returns every subscription owned by a user (a user may be signed
// in on several browsers/devices).
func (d *DB) ListPushSubs(userID string) ([]PushSub, error) {
	var subs []PushSub
	err := d.db.Select(&subs,
		"SELECT endpoint, user_id, p256dh, auth FROM push_subscriptions WHERE user_id = ?", userID)
	return subs, err
}

// DeletePushSub removes one of a user's subscriptions by endpoint (browser
// unsubscribe). Scoped to user_id so a request can't delete another user's row.
func (d *DB) DeletePushSub(userID, endpoint string) (bool, error) {
	res, err := d.db.Exec(
		"DELETE FROM push_subscriptions WHERE user_id = ? AND endpoint = ?", userID, endpoint)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeletePushSubByEndpoint removes a subscription regardless of owner. Used to
// prune a dead endpoint after the push service reports it Gone (404/410).
func (d *DB) DeletePushSubByEndpoint(endpoint string) error {
	_, err := d.db.Exec("DELETE FROM push_subscriptions WHERE endpoint = ?", endpoint)
	return err
}
