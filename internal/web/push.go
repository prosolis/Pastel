package web

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/prosolis/Pastel/internal/database"
)

// validPushKey reports whether a base64url (unpadded) key decodes to exactly
// wantLen bytes — the sizes RFC 8291 requires (a 65-byte uncompressed P-256
// point for p256dh, a 16-byte auth secret). The webpush sender decodes with the
// same alphabet, so a key that fails here could never be delivered to anyway;
// rejecting it up front keeps undeliverable rows out of the DB.
func validPushKey(s string, wantLen int) bool {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == wantLen
}

// pushSubBody is the wire shape of a browser PushSubscription.toJSON(): the push
// service endpoint plus the client's encryption keys.
type pushSubBody struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// handlePushConfig serves GET /api/push/config — whether Web Push is available
// and, if so, the VAPID public key the service worker subscribes with.
func (s *Server) handlePushConfig(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"enabled": s.push != nil, "publicKey": ""}
	if s.push != nil {
		resp["publicKey"] = s.push.PublicKey()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handlePushSubscribe serves POST /api/push/subscribe, storing the caller's
// browser subscription so deal alerts can be delivered to it.
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request, sess *database.WebSession) {
	if s.push == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "push not enabled"})
		return
	}
	var body pushSubBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	// Endpoints are https URLs; the keys are base64url of fixed length. Reject
	// anything malformed before it reaches the DB / push path, so a bad row can't
	// linger and fail (un-prunably) on every future send.
	if !strings.HasPrefix(body.Endpoint, "https://") || !validPushKey(body.Keys.P256dh, 65) || !validPushKey(body.Keys.Auth, 16) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid subscription"})
		return
	}
	if err := s.db.SavePushSub(sess.UserID, body.Endpoint, body.Keys.P256dh, body.Keys.Auth); err != nil {
		slog.Error("web: save push subscription failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save subscription"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscribed": true})
}

// handlePushUnsubscribe serves POST /api/push/unsubscribe, removing one of the
// caller's stored subscriptions by endpoint.
func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request, sess *database.WebSession) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Endpoint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endpoint is required"})
		return
	}
	removed, err := s.db.DeletePushSub(sess.UserID, body.Endpoint)
	if err != nil {
		slog.Error("web: remove push subscription failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove subscription"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}
