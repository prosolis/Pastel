// Package web serves the Pastel browsing/watchlist web interface. It runs as an
// in-process HTTP server inside the bot, sharing the same SQLite database.
package web

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/prosolis/Pastel/internal/config"
	"github.com/prosolis/Pastel/internal/database"
	"github.com/prosolis/Pastel/internal/watchlist"
)

//go:embed static
var staticFS embed.FS

func init() {
	// Go's built-in MIME table doesn't know AVIF, and a minimal deploy host may
	// lack /etc/mime.types, so register it explicitly for correct Content-Type.
	_ = mime.AddExtensionType(".avif", "image/avif")
}

// Server holds the dependencies the web handlers need.
type Server struct {
	cfg   *config.Config
	db    *database.DB
	watch *watchlist.Store
	mux   *http.ServeMux

	authMu sync.Mutex     // guards lazy authenticator initialization
	auth   *authenticator // nil until OIDC discovery succeeds
}

// New constructs a web server wired to the shared database and watchlist store.
func New(cfg *config.Config, db *database.DB, watch *watchlist.Store) *Server {
	s := &Server{cfg: cfg, db: db, watch: watch, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	// Read-only deal browsing API.
	s.mux.HandleFunc("GET /api/deals", s.handleDeals)
	s.mux.HandleFunc("GET /api/facets", s.handleFacets)
	s.mux.HandleFunc("GET /api/me", s.handleMe)

	// Auth-gated watchlist API.
	s.mux.HandleFunc("GET /api/watchlist", s.requireAuth(s.handleWatchlistGet))
	s.mux.HandleFunc("POST /api/watchlist", s.requireAuth(s.handleWatchlistPost))
	s.mux.HandleFunc("DELETE /api/watchlist", s.requireAuth(s.handleWatchlistDelete))

	// Authentik OIDC (Authorization Code + PKCE).
	s.mux.HandleFunc("GET /auth/login", s.handleLogin)
	s.mux.HandleFunc("GET /auth/callback", s.handleCallback)
	s.mux.HandleFunc("POST /auth/logout", s.handleLogout)

	// Static frontend. Serve the embedded "static" directory at the root.
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// staticFS is embedded at build time, so this cannot fail in practice.
		panic(err)
	}
	s.mux.Handle("/", http.FileServer(http.FS(sub)))
}

// Handler returns the root HTTP handler (useful for tests).
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Run starts the HTTP server and blocks until the context is cancelled, at
// which point it shuts down gracefully. Intended to be launched in a goroutine.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.WebListenAddr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("web server shutdown error", "error", err)
		}
	}()

	// Periodically prune expired web sessions.
	go func() {
		if err := s.db.PruneSessions(); err != nil {
			slog.Warn("web: prune sessions failed", "error", err)
		}
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.db.PruneSessions(); err != nil {
					slog.Warn("web: prune sessions failed", "error", err)
				}
			}
		}
	}()

	slog.Info("web server listening", "addr", s.cfg.WebListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
