package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// addWatch is a small helper that POSTs a game to the watchlist.
func addWatch(t *testing.T, s *Server, cookie *http.Cookie, game string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/watchlist", strings.NewReader(`{"game":"`+game+`"}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestWatchlistAddListRemove(t *testing.T) {
	s, cookie := newAuthedServer(t, "@alice:example.com")

	// Add a watch.
	if rec := addWatch(t, s, cookie, "Hollow Knight"); rec.Code != http.StatusOK {
		t.Fatalf("add status = %d", rec.Code)
	}

	// Adding the same game again should report added=false.
	rec := addWatch(t, s, cookie, "hollow knight")
	var addResp struct{ Added bool `json:"added"` }
	_ = json.Unmarshal(rec.Body.Bytes(), &addResp)
	if addResp.Added {
		t.Fatalf("duplicate add should report added=false")
	}

	// List should contain exactly one entry.
	listReq := httptest.NewRequest(http.MethodGet, "/api/watchlist", nil)
	listReq.AddCookie(cookie)
	listRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(listRec, listReq)

	var list struct {
		Watches []watchJSON `json:"watches"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Watches) != 1 || list.Watches[0].GameName != "Hollow Knight" {
		t.Fatalf("unexpected watches: %+v", list.Watches)
	}
	if list.Watches[0].ExpiresAt == "" {
		t.Fatalf("expected expiresAt to be set")
	}

	// Remove it by ID.
	id := list.Watches[0].ID
	delReq := httptest.NewRequest(http.MethodDelete, "/api/watchlist?id="+strconv.FormatInt(id, 10), nil)
	delReq.AddCookie(cookie)
	delRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(delRec, delReq)

	var delResp struct{ Removed bool `json:"removed"` }
	_ = json.Unmarshal(delRec.Body.Bytes(), &delResp)
	if !delResp.Removed {
		t.Fatalf("expected removed=true, got body %s", delRec.Body.String())
	}
}

func TestWatchlistRemoveByGame(t *testing.T) {
	s, cookie := newAuthedServer(t, "@alice:example.com")
	addWatch(t, s, cookie, "Celeste")

	req := httptest.NewRequest(http.MethodDelete, "/api/watchlist?game=celeste", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	var resp struct{ Removed bool `json:"removed"` }
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Removed {
		t.Fatalf("expected removed=true, got body %s", rec.Body.String())
	}
}

func TestWatchlistRequiresAuth(t *testing.T) {
	s, _ := newAuthedServer(t, "@alice:example.com")

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/watchlist", nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d", method, rec.Code)
		}
	}
}

func TestWatchlistAddRejectsEmpty(t *testing.T) {
	s, cookie := newAuthedServer(t, "@alice:example.com")
	rec := addWatch(t, s, cookie, "   ")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty game, got %d", rec.Code)
	}
}
