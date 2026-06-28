package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/prosolis/Pastel/internal/database"
	"github.com/prosolis/Pastel/internal/webpush"
)

// pushOut delivers Web Push notifications to subscribed browsers (Phase 5). It is
// a single process-wide handle established at startup and consulted by
// notifyWatchlist; nil when the web server / push is disabled, in which case all
// push fan-out is silently skipped. Keeping it package-level avoids threading a
// sender through every deal-check function for one optional side channel.
var pushOut *webPush

type webPush struct {
	sender *webpush.Sender
	db     *database.DB
}

// setupPush loads the persisted VAPID keypair, generating and saving one on first
// run, and returns a ready webPush. Returns nil (push disabled) on any failure so
// the bot still runs without notifications.
func setupPush(db *database.DB, subject string) *webPush {
	priv, err := db.GetConfig("vapid_private_key")
	if err != nil || priv == "" {
		newPriv, pub, genErr := webpush.GenerateVAPIDKeys()
		if genErr != nil {
			slog.Error("push: failed to generate VAPID keys", "error", genErr)
			return nil
		}
		if err := db.SetConfig("vapid_private_key", newPriv); err != nil {
			slog.Error("push: failed to persist VAPID private key", "error", err)
			return nil
		}
		// The public key is stored only for operator visibility; the sender
		// re-derives it from the private scalar.
		_ = db.SetConfig("vapid_public_key", pub)
		priv = newPriv
		slog.Info("push: generated new VAPID keypair")
	}

	sender, err := webpush.NewSender(priv, subject)
	if err != nil {
		slog.Error("push: failed to init sender", "error", err)
		return nil
	}
	return &webPush{sender: sender, db: db}
}

// notify pushes one notification to every browser subscription owned by userID,
// pruning any endpoint the push service reports as permanently gone. Best-effort:
// a single failed endpoint never blocks the others.
func (wp *webPush) notify(userID, title, body, url string) {
	subs, err := wp.db.ListPushSubs(userID)
	if err != nil {
		slog.Warn("push: list subscriptions failed", "user", userID, "error", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	payload, err := json.Marshal(map[string]string{"title": title, "body": body, "url": url})
	if err != nil {
		return
	}

	for _, s := range subs {
		// One timeout per send so a single slow endpoint can't starve the rest of
		// a user's subscriptions of their delivery budget.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		status, err := wp.sender.Send(ctx, webpush.Subscription{
			Endpoint: s.Endpoint,
			P256dh:   s.P256dh,
			Auth:     s.Auth,
		}, payload)
		cancel()
		switch {
		case err != nil:
			slog.Warn("push: send failed", "user", userID, "error", err)
		case webpush.Gone(status):
			if err := wp.db.DeletePushSubByEndpoint(s.Endpoint); err != nil {
				slog.Warn("push: prune dead subscription failed", "error", err)
			}
		case status < 200 || status >= 300:
			// 4xx/5xx that aren't Gone (e.g. 401 bad VAPID, 413 too large, 429
			// rate-limit, 5xx) would otherwise be silently treated as delivered.
			slog.Warn("push: unexpected status", "user", userID, "status", status, "endpoint", s.Endpoint)
		}
	}
}
