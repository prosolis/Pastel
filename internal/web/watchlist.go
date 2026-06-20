package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prosolis/Pastel/internal/database"
)

// watchJSON is the wire shape for a single watchlist entry.
type watchJSON struct {
	ID        int64  `json:"id"`
	GameName  string `json:"gameName"`
	ExpiresAt string `json:"expiresAt"`
}

// handleWatchlistGet serves GET /api/watchlist — the caller's active watches.
func (s *Server) handleWatchlistGet(w http.ResponseWriter, r *http.Request, sess *database.WebSession) {
	entries, err := s.watch.ListWatches(sess.UserID)
	if err != nil {
		slog.Error("web: list watches failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load watchlist"})
		return
	}

	watches := make([]watchJSON, 0, len(entries))
	for _, e := range entries {
		watches = append(watches, watchJSON{
			ID:        e.ID,
			GameName:  e.GameName,
			ExpiresAt: e.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"watches": watches})
}

// handleWatchlistPost serves POST /api/watchlist with a JSON body {"game": "..."}.
func (s *Server) handleWatchlistPost(w http.ResponseWriter, r *http.Request, sess *database.WebSession) {
	var body struct {
		Game string `json:"game"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	game := strings.TrimSpace(body.Game)
	if game == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "game is required"})
		return
	}

	added, err := s.watch.AddWatch(sess.UserID, game)
	if err != nil {
		slog.Error("web: add watch failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add watch"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added})
}

// handleWatchlistDelete serves DELETE /api/watchlist?id= (or ?game=).
func (s *Server) handleWatchlistDelete(w http.ResponseWriter, r *http.Request, sess *database.WebSession) {
	q := r.URL.Query()

	var (
		removed bool
		err     error
	)
	switch {
	case q.Get("id") != "":
		id, convErr := strconv.ParseInt(q.Get("id"), 10, 64)
		if convErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		removed, err = s.watch.RemoveWatchByID(sess.UserID, id)
	case q.Get("game") != "":
		removed, err = s.watch.RemoveWatch(sess.UserID, q.Get("game"))
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id or game is required"})
		return
	}

	if err != nil {
		slog.Error("web: remove watch failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove watch"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}
