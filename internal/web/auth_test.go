package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prosolis/Pastel/internal/config"
	"github.com/prosolis/Pastel/internal/database"
	"github.com/prosolis/Pastel/internal/watchlist"
)

// newAuthedServer returns a server plus a valid session cookie for userID.
func newAuthedServer(t *testing.T, userID string) (*Server, *http.Cookie) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.CreateSession("tok-123", userID, "Test User", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	cfg := &config.Config{WebListenAddr: ":0"}
	s := New(cfg, db, watchlist.NewStore(db.RawDB()), nil)
	return s, &http.Cookie{Name: sessionCookie, Value: "tok-123"}
}

func TestMeAuthenticated(t *testing.T) {
	s, cookie := newAuthedServer(t, "@alice:example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["authenticated"] != true {
		t.Fatalf("expected authenticated, got %v", got["authenticated"])
	}
	if got["userId"] != "@alice:example.com" {
		t.Fatalf("userId = %v", got["userId"])
	}
}

func TestMeWithExpiredSession(t *testing.T) {
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.CreateSession("old", "@bob:example.com", "Bob", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	s := New(&config.Config{}, db, watchlist.NewStore(db.RawDB()), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "old"})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["authenticated"] != false {
		t.Fatalf("expired session should be unauthenticated, got %v", got["authenticated"])
	}
}

func TestLogoutClearsSession(t *testing.T) {
	s, cookie := newAuthedServer(t, "@alice:example.com")

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	// The session row should be gone.
	sess, err := s.db.GetSession("tok-123")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess != nil {
		t.Fatalf("session should have been deleted")
	}
}

func TestLoginUnavailableWithoutOIDC(t *testing.T) {
	s, _ := newAuthedServer(t, "@alice:example.com") // cfg has no OIDC settings

	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when OIDC unconfigured, got %d", rec.Code)
	}
}

func TestRequireAuthRejectsAnonymous(t *testing.T) {
	s, _ := newAuthedServer(t, "@alice:example.com")
	called := false
	h := s.requireAuth(func(w http.ResponseWriter, r *http.Request, sess *database.WebSession) {
		called = true
	})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/watchlist", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatalf("handler should not run for anonymous request")
	}
}
