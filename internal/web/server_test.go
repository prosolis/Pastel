package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prosolis/Pastel/internal/config"
	"github.com/prosolis/Pastel/internal/database"
	"github.com/prosolis/Pastel/internal/watchlist"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.SaveDeal(database.Deal{
		DedupID: "cs-1", Source: "cheapshark", Kind: "game",
		Title: "Hollow Knight", TitleNorm: "hollow knight", Store: "Steam",
		SalePrice: 7.49, NormalPrice: 14.99, Discount: 50, Rating: 9.5,
		URL: "https://example.com/hk", IsHistLow: true,
	}); err != nil {
		t.Fatalf("save deal: %v", err)
	}

	cfg := &config.Config{WebListenAddr: ":0"}
	return New(cfg, db, watchlist.NewStore(db.RawDB()), nil)
}

func TestDealsEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/deals?q=hollow", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Deals []database.Deal `json:"deals"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 1 || len(got.Deals) != 1 {
		t.Fatalf("expected 1 deal, got total=%d len=%d", got.Total, len(got.Deals))
	}
	if got.Deals[0].Title != "Hollow Knight" || !bool(got.Deals[0].IsHistLow) {
		t.Fatalf("unexpected deal: %+v", got.Deals[0])
	}
}

func TestFacetsEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/facets", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Sources []string `json:"sources"`
		Stores  []string `json:"stores"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Sources) != 1 || got.Sources[0] != "cheapshark" {
		t.Fatalf("sources = %v", got.Sources)
	}
	if len(got.Stores) != 1 || got.Stores[0] != "Steam" {
		t.Fatalf("stores = %v", got.Stores)
	}
}

func TestMeEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/me", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["authenticated"] != false {
		t.Fatalf("expected unauthenticated, got %v", got["authenticated"])
	}
}

func TestStaticIndexServed(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("expected content-type for index")
	}
}

func TestMascotAvifServed(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pastel.avif", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/avif" {
		t.Fatalf("content-type = %q, want image/avif", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("empty avif body")
	}
}
