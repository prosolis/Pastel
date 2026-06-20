package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/prosolis/Pastel/internal/database"
)

const (
	sessionCookie  = "pastel_session"
	stateCookie    = "pastel_oauth_state"
	verifierCookie = "pastel_oauth_verifier"
	sessionTTL     = 7 * 24 * time.Hour
)

// authenticator holds the OIDC/OAuth2 machinery once provider discovery has
// succeeded. It is created lazily so the rest of the site keeps working even if
// the identity provider is unreachable at startup.
type authenticator struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

// oidcConfigured reports whether enough config is present to attempt OIDC.
func (s *Server) oidcConfigured() bool {
	return s.cfg.OIDCIssuerURL != "" &&
		s.cfg.OIDCClientID != "" &&
		s.cfg.OIDCClientSecret != "" &&
		s.cfg.WebPublicURL != ""
}

// ensureAuth lazily performs OIDC provider discovery. It is safe for concurrent
// use and non-fatal: a discovery failure returns an error and leaves the site
// browsable without login.
func (s *Server) ensureAuth() (*authenticator, error) {
	if !s.oidcConfigured() {
		return nil, fmt.Errorf("OIDC is not configured")
	}

	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.auth != nil {
		return s.auth, nil
	}

	provider, err := oidc.NewProvider(context.Background(), s.cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}

	a := &authenticator{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: s.cfg.OIDCClientID}),
		oauth: &oauth2.Config{
			ClientID:     s.cfg.OIDCClientID,
			ClientSecret: s.cfg.OIDCClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  strings.TrimRight(s.cfg.WebPublicURL, "/") + "/auth/callback",
			Scopes:       []string{oidc.ScopeOpenID, "profile"},
		},
	}
	s.auth = a
	return a, nil
}

// randToken returns a URL-safe random string with the given byte length.
func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// secureCookies reports whether cookies should carry the Secure flag, inferred
// from the public URL scheme.
func (s *Server) secureCookies() bool {
	return strings.HasPrefix(strings.ToLower(s.cfg.WebPublicURL), "https://")
}

// handleLogin starts the Authorization Code + PKCE flow.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	auth, err := s.ensureAuth()
	if err != nil {
		slog.Warn("web: login unavailable", "error", err)
		http.Error(w, "login is unavailable", http.StatusServiceUnavailable)
		return
	}

	state, err := randToken(24)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	verifier := oauth2.GenerateVerifier()

	s.setShortCookie(w, stateCookie, state)
	s.setShortCookie(w, verifierCookie, verifier)

	url := auth.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, url, http.StatusFound)
}

// handleCallback completes the flow: validates state, exchanges the code,
// verifies the ID token, derives the Matrix ID, and creates a session.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	auth, err := s.ensureAuth()
	if err != nil {
		http.Error(w, "login is unavailable", http.StatusServiceUnavailable)
		return
	}

	// CSRF: the state in the query must match the state cookie.
	stateC, err := r.Cookie(stateCookie)
	if err != nil || stateC.Value == "" || stateC.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	verifierC, err := r.Cookie(verifierCookie)
	if err != nil || verifierC.Value == "" {
		http.Error(w, "missing PKCE verifier", http.StatusBadRequest)
		return
	}
	// One-shot cookies: clear them now.
	s.clearCookie(w, stateCookie)
	s.clearCookie(w, verifierCookie)

	ctx := r.Context()
	token, err := auth.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(verifierC.Value))
	if err != nil {
		slog.Warn("web: token exchange failed", "error", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		http.Error(w, "no id_token in response", http.StatusUnauthorized)
		return
	}
	idToken, err := auth.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		slog.Warn("web: id_token verification failed", "error", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "could not read claims", http.StatusInternalServerError)
		return
	}
	if claims.PreferredUsername == "" {
		http.Error(w, "no preferred_username claim", http.StatusUnauthorized)
		return
	}

	// Derive the Matrix user ID: @{preferred_username}:{server name}.
	userID := fmt.Sprintf("@%s:%s", claims.PreferredUsername, s.cfg.MatrixServerName)
	displayName := claims.Name
	if displayName == "" {
		displayName = claims.PreferredUsername
	}

	sessToken, err := randToken(32)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.db.CreateSession(sessToken, userID, displayName, time.Now().Add(sessionTTL)); err != nil {
		slog.Error("web: failed to create session", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
		MaxAge:   int(sessionTTL.Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleLogout deletes the current session and clears the cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := s.db.DeleteSession(c.Value); err != nil {
			slog.Warn("web: failed to delete session", "error", err)
		}
	}
	s.clearCookie(w, sessionCookie)
	w.WriteHeader(http.StatusNoContent)
}

// currentSession returns the valid session for the request, or nil if the
// requester is not authenticated.
func (s *Server) currentSession(r *http.Request) *database.WebSession {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	sess, err := s.db.GetSession(c.Value)
	if err != nil {
		slog.Warn("web: session lookup failed", "error", err)
		return nil
	}
	return sess
}

// requireAuth wraps a handler so it only runs for authenticated requests,
// passing the session through the request context.
func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request, *database.WebSession)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := s.currentSession(r)
		if sess == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		next(w, r, sess)
	}
}

func (s *Server) setShortCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((10 * time.Minute).Seconds()),
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
