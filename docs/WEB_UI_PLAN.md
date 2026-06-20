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
- [ ] **M2 — Config + HTTP server skeleton + read-only API + static embed + frontend shell**
  - Config: `WEB_ENABLED`, `WEB_LISTEN_ADDR` (default `:8080`), `WEB_PUBLIC_URL`,
    `MATRIX_SERVER_NAME` (default = domain of `MATRIX_BOT_USER_ID`), `OIDC_ISSUER_URL`,
    `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`.
  - `internal/web`: server, routes, `//go:embed static`.
  - `GET /api/deals` (query params: q, source, store, kind, min_discount, max_price,
    hist_low, free, sort, limit, offset), `GET /api/facets`, `GET /api/me`.
  - Minimal static shell so deals render. Start server goroutine from `main.go`
    when `WEB_ENABLED`.
- [ ] **M3 — Authentik OIDC**
  - `oidc.NewProvider` discovery (lazy/non-fatal if it fails — browsing still works).
  - `GET /auth/login` (state + PKCE, state cookie), `GET /auth/callback`
    (verify id_token, derive Matrix ID, create `web_sessions` row, set httponly cookie),
    `POST /auth/logout`. Session middleware. Session methods on `DB`
    (CreateSession/GetSession/DeleteSession/PruneSessions).
- [ ] **M4 — Watchlist API + UI integration**
  - Auth-gated `GET/POST/DELETE /api/watchlist` backed by the existing
    `watchlist.Store` (keyed by the derived Matrix user ID).
  - "★ Watch" button on cards; watchlist drawer/panel.
- [ ] **M5 — The "hella cute" pass**
  - Animated gradient background, candy cards, discount-burst badges, springy
    hover, staggered Web-Animations entrance, confetti on watch, mascot, skeleton
    loaders, cute empty state, responsive, reduced-motion fallback.
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
