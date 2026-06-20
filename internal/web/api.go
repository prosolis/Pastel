package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/prosolis/Pastel/internal/database"
)

// writeJSON serializes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("web: failed to encode JSON response", "error", err)
	}
}

// csv splits a comma-separated query parameter into trimmed, non-empty values.
func csv(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// queryBool reports whether a query parameter is set to a truthy value.
func queryBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// handleDeals serves GET /api/deals with filtering, sorting, and pagination.
func (s *Server) handleDeals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	minDiscount, _ := strconv.Atoi(q.Get("min_discount"))
	maxPrice, _ := strconv.ParseFloat(q.Get("max_price"), 64)

	filter := database.DealFilter{
		Query:       q.Get("q"),
		Sources:     csv(q.Get("source")),
		Stores:      csv(q.Get("store")),
		Kinds:       csv(q.Get("kind")),
		MinDiscount: minDiscount,
		MaxPrice:    maxPrice,
		HistLowOnly: queryBool(q.Get("hist_low")),
		FreeOnly:    queryBool(q.Get("free")),
		Sort:        q.Get("sort"),
		Limit:       limit,
		Offset:      offset,
	}

	deals, total, err := s.db.QueryDeals(filter)
	if err != nil {
		slog.Error("web: query deals failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to query deals"})
		return
	}
	if deals == nil {
		deals = []database.Deal{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deals": deals,
		"total": total,
		// Report the limit actually applied, not the raw request value: QueryDeals
		// clamps an unset/out-of-range limit to a default, so echoing filter.Limit
		// (0 when unset) would mislead a paginating client.
		"limit":  database.ClampDealLimit(filter.Limit),
		"offset": filter.Offset,
	})
}

// handleFacets serves GET /api/facets — the distinct sources and stores.
func (s *Server) handleFacets(w http.ResponseWriter, r *http.Request) {
	sources, stores, err := s.db.DealFacets()
	if err != nil {
		slog.Error("web: facets failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load facets"})
		return
	}
	if sources == nil {
		sources = []string{}
	}
	if stores == nil {
		stores = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": sources,
		"stores":  stores,
	})
}

// handleMe serves GET /api/me, reporting the current auth state plus whether
// OIDC login is available at all.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"authenticated": false,
		"userId":        "",
		"displayName":   "",
		"oidcEnabled":   s.oidcConfigured(),
	}
	if sess := s.currentSession(r); sess != nil {
		resp["authenticated"] = true
		resp["userId"] = sess.UserID
		resp["displayName"] = sess.DisplayName
	}
	writeJSON(w, http.StatusOK, resp)
}
