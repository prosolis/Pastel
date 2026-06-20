# Pastel Web UI — Multi-Session Build Plan

A cute, bubbly, **highly animated** web interface where users browse, search, and
filter deals, and manage their watchlist. This is a multi-session effort; this
document is the source of truth for decisions and progress.

## Locked decisions

| Decision | Choice |
|----------|--------|
| Frontend | Hand-written HTML/CSS/JS embedded via Go `embed`. **No Node/build step.** Single binary. |
| Aesthetic | **"Hella cute, bubbly, highly animated."** Pastel palette (lean into the project name), candy/rounded cards, springy hover, staggered entrance animations, confetti on watch, cute mascot + empty/loading states. This is a hard requirement, not a nice-to-have. |
| Scope | Browse / search / filter **and** watchlist management (add/remove from the web). |
| Hosting | In-process: an HTTP server goroutine inside the bot, gated by `WEB_ENABLED` / `WEB_LISTEN_ADDR`. Shares the same SQLite DB. |
| Auth | **Authentik OIDC only** — Authorization Code + PKCE, server-side session cookie. Libraries: `golang.org/x/oauth2` + `github.com/coreos/go-oidc/v3`. |
| Identity mapping | Derive the Matrix user ID from the OIDC `preferred_username` + the homeserver domain: `@{preferred_username}:{MATRIX_SERVER_NAME}`. Authentik users were mass-imported from Matrix, so usernames match for *almost* everyone. `MATRIX_SERVER_NAME` defaults to the domain parsed from `MATRIX_BOT_USER_ID`. Edge case (mismatched usernames) is acceptable for v1 — document it; a later "link your Matrix ID" step can be added if needed. |

## Architecture notes

- The bot historically stored **only dedup metadata** (`posted_deals`: id, source,
  title, posted_at) and threw away price/discount/URL/etc. The web UI needs rich
  data, so a new `deals` table holds the full record. `posted_deals` remains the
  source of truth for dedup/posting; `deals` is a display superset.
- Deals are saved for **every current filtered deal** each poll (not just
  newly-posted ones), so the gallery reflects what's on offer now. Pruned at 30
  days like `posted_deals`.
- Known limitation: ITAD prices are stored in their original currency (matches
  the existing bot behavior, which already filters ITAD by price as if USD).
  Revisit for correct multi-currency display in a later session.

## Milestones

- [x] **M1 — Data persistence backbone** *(done)*
  - `deals` table + `web_sessions` table in `internal/database/database.go`.
  - `database.Bool` type (SQLite int <-> Go bool <-> JSON bool; verified with modernc driver).
  - `Deal` struct, `SaveDeal` (upsert), `QueryDeals` (filters + pagination + total),
    `DealFacets`, `PruneDealsTable`.
  - Pipeline wiring: `cmd/pastel/persist.go` converters; called in
    `checkCheapShark`/`checkITADDeals`/`checkEpicFreeGames` + `populateInitialState`.
  - Round-trip test in `internal/database/database_test.go`.
- [x] **M2 — Config + HTTP server skeleton + read-only API + static embed + frontend shell** *(done)*
  - Config (`internal/config/config.go`): `WEB_ENABLED`, `WEB_LISTEN_ADDR` (default `:8080`),
    `WEB_PUBLIC_URL`, `MATRIX_SERVER_NAME` (default = domain parsed from `MATRIX_BOT_USER_ID`
    via `serverNameFromUserID`), `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`.
    Added `envBool` helper.
  - `internal/web/server.go`: `Server` struct, `New`, `routes`, `Handler` (for tests),
    `Run(ctx)` with graceful shutdown; `//go:embed static`.
  - `internal/web/api.go`: `GET /api/deals` (q, source, store, kind, min_discount,
    max_price, hist_low, free, sort, limit, offset → `{deals,total,limit,offset}`),
    `GET /api/facets`, `GET /api/me` (stub: always unauthenticated, reports `oidcEnabled`).
  - `internal/web/static/`: `index.html`, `style.css`, `app.js` — functional gallery
    with filters/sort/pagination (pastel-themed but the animated pass is M5).
  - Wired into `cmd/pastel/main.go`: web server started in a goroutine under a
    cancelable context when `WEB_ENABLED`; cancelled on shutdown signal.
  - `internal/web/server_test.go`: httptest coverage for all 3 endpoints + static index.
- [x] **M3 — Authentik OIDC** *(done)*
  - DB session methods (`internal/database/database.go`): `WebSession` struct,
    `CreateSession`, `GetSession`, `DeleteSession`, `PruneSessions`. **Gotcha fixed:**
    `expires_at` is stored RFC3339 (`T` separator) which does NOT order correctly
    against SQLite `CURRENT_TIMESTAMP` (`YYYY-MM-DD HH:MM:SS`), so `GetSession`
    compares against a Go-formatted RFC3339 `now` instead (same pattern watchlist
    already uses in `PurgeExpired`).
  - `internal/web/auth.go`: lazy, non-fatal `oidc.NewProvider` discovery
    (`ensureAuth`, guarded by `authMu`); `GET /auth/login` (state cookie + PKCE
    S256 via `oauth2.GenerateVerifier`), `GET /auth/callback` (state check, code
    exchange w/ verifier, id_token verify, derive `@{preferred_username}:{MATRIX_SERVER_NAME}`,
    create session, set httponly session cookie), `POST /auth/logout`.
    `currentSession` helper + `requireAuth` middleware (for M4). Cookies get the
    Secure flag when `WEB_PUBLIC_URL` is https.
  - `/api/me` now reports real auth state. Session pruning runs hourly in `Run`.
  - Frontend: sign in / sign out control in the topbar.
  - Deps added: `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`.
  - Tests: `internal/web/auth_test.go` (authed /api/me, expired session, logout,
    login-unavailable-without-OIDC, requireAuth rejects anonymous).
- [x] **M4 — Watchlist API + UI integration** *(done)*
  - DB/store: added `watchlist.Store.RemoveWatchByID(userID, id)` (user-scoped delete).
  - `internal/web/watchlist.go`: auth-gated (`requireAuth`) `GET /api/watchlist`
    (`{watches:[{id,gameName,expiresAt}]}`), `POST /api/watchlist` (JSON `{game}`,
    `MaxBytesReader` 4 KiB, returns `{added}`), `DELETE /api/watchlist?id=` or
    `?game=` (returns `{removed}`). Keyed by `sess.UserID`. Routes wired in `server.go`.
  - Frontend: `★ Watch`/`★ Watching` toggle on cards (only when authed), watchlist
    drawer (topbar `★ Watchlist` button → slide-in panel w/ add field + remove
    buttons + scrim). JS `normalize()` mirrors `watchlist.Normalize` for optimistic
    state; server stays source of truth. `watched` Map (normName→id) drives toggles.
  - Tests: `internal/web/watchlist_test.go` (add/list/remove-by-id, remove-by-game,
    requires-auth for all 3 verbs, rejects-empty). All pass; `go build`/`vet` clean.
- [x] **M5 — The "hella cute" pass** *(done)*
  - **WebGL animated background** (`startWebGLBackground`): full-screen domain-warped
    pastel fragment shader. Graceful fallback ladder: WebGL → animated canvas-2D
    drifting orbs (`start2DBackground`) → CSS `.bg-fallback` blurred blobs. Disabled
    under `prefers-reduced-motion`.
  - Candy cards: gradient rim (mask trick), hover gloss sweep, springy lift; discount
    badge `pop`, gold historical-low `shine`, free-green gradient.
  - Confetti (`burstConfetti`, Web Animations API) fires from the toggled ★ button /
    drawer add field; mascot does a squash-stretch `boing`. Mascot = the real Pastel
    chibi (`pastel.avif`, AVIF-encoded from `pastel.webp` via avifenc -q88; `<picture>`
    with WebP fallback) — bobbing + clickable in the topbar, plus the deals empty state
    and watchlist empty state. `.avif` MIME registered in a `server.go` `init()` (Go's
    builtin table omits it). Test: `TestMascotAvifServed`.
  - Skeleton shimmer cards while a query is in flight (`renderSkeletons`); staggered
    spring entrance on results (`playEntrance`). Glassmorphism topbar/filters/drawer,
    slide-in drawer, focus rings.
  - Reduced-motion media query neutralises all animation; `REDUCE_MOTION` guards the
    JS effects too. Responsive single-column under 760px.
  - Typography: **Mochiy Pop One** (logo + card titles + prices + drawer heading) +
    **Nunito** (body) via Google Fonts (`display=swap`, system-ui fallback) — only
    external runtime dependency; page is fully usable offline. Mochiy is single-weight,
    so heading rules carry no `font-weight` bump (avoids faux-bold synthesis). Chosen by
    the user after a headless-chromium render of 6 candidate pairings.
  - `go build`/`vet`/`test ./internal/web` all green; `node --check app.js` clean.
- [ ] **M6 — Docs + deploy**
  - `.env.example` + README web section, systemd unit exposure/port notes,
    reverse-proxy guidance for `WEB_PUBLIC_URL` behind Authentik.

## API shapes (target)

```
GET /api/deals  -> { deals: [Deal], total, limit, offset }
GET /api/facets -> { sources: [string], stores: [string] }
GET /api/me     -> { authenticated: bool, userId, displayName, oidcEnabled: bool }
GET /api/watchlist            -> { watches: [{ id, gameName, expiresAt }] }   (auth)
POST /api/watchlist {game}    -> add                                          (auth)
DELETE /api/watchlist?id=     -> remove                                       (auth)
```

`Deal` JSON: `{ id, source, kind, title, store, salePrice, normalPrice, discount,
rating, url, isHistLow, isFree, upcoming, expiresAt?, postedAt }`.
