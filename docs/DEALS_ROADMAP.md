# Pastel — Deals Service Roadmap

Roadmap to make Pastel the best deals service for Parodia members. Pastel is
already strong at *plumbing* (multi-source fetch, dedup, a 180-day Matrix
watchlist, a pastel web gallery with OIDC auth). These phases add the layer that
makes a deals service *worth opening*: **trust**, **personalization**,
**community**, and **coverage**.

This document is the source of truth for the roadmap and its progress. Each
phase ships working and is verified/committed before the next starts (roughly
one phase per work session). Phases are designed concretely here but later
phases are tightened immediately before their build.

## Status

| Phase | Title | Status |
|-------|-------|--------|
| 1 | Price verdicts & trust badges | ✅ Done — 2026-06-28 (`4a9fc57`) |
| 2 | Watchlist 2.0 | ✅ Done — 2026-06-28 (`49b6daf`) |
| 3 | Community layer | ✅ Done — 2026-06-28 |
| 4 | Deal images | ✅ Done — 2026-06-28 |
| 5 | Coverage & reach | ✅ Done — 2026-06-28 (free super-tab, DealNews verticals, PWA + Web Push) |

Branch: `feat/price-verdicts`.

## Architecture facts the roadmap relies on

- **DB:** SQLite, schema built in `internal/database/database.go` `migrate()`
  (`CREATE TABLE IF NOT EXISTS` + idempotent `addColumnIfMissing(table, column,
  ddl)`). No migration framework — additive only. Every `migrate()` runs on
  startup and must stay idempotent.
- `deals` table is a display superset; `SaveDeal`/`SaveDealWithVerdict` upsert
  via `ON CONFLICT(dedup_id) DO UPDATE`. `posted_deals` remains the source of
  truth for dedup/posting.
- **Web:** `/api/deals` → `handleDeals` (`internal/web/api.go`) →
  `QueryDeals(DealFilter)`. Frontend cards in `internal/web/static/app.js`
  `cardHTML()`, badge CSS in `style.css`.
- **Matrix:** posts via `internal/matrix/client.go` `SendDealInThread`; only
  `EventMessage` is handled (`RegisterMessageHandler`). **No reaction handling
  and no deal→event_id mapping exist yet** (Phase 3 prerequisite).
- Prices are `REAL` USD; `is_hist_low` comes from the ITAD flag and is only set
  for game sources.

---

## Phase 1 — Price verdicts & trust badges  ✅

**Goal:** Every deal gets a trust verdict (`all-time-low` / `good` / `meh`) plus
a fake-discount warning, backed by Pastel's *own* observed price history (so it
works for RSS deals with no ITAD data). Surfaced as badges + a filter.

### Built

- **Schema:** new `price_history(price_key, source, sale_price, seen_at)` table;
  additive `deals` columns `verdict TEXT`, `price_low REAL`, `price_suspect INTEGER`.
  Pruned alongside the existing deal pruning (~180 days).
- **Price key:** `dedup_id` changes when discount changes, so history is keyed by
  a stable `PriceKey(deal)` = `category + "|" + title_normalized` (game keys can
  add the Steam app id).
- **Verdict logic** lives in `internal/database/verdict.go` (kept out of
  `internal/deals` so its test binary doesn't link the `matrix` pkg, which
  double-registers the `sqlite3-fk-wal` driver and panics under `go test`):
  - `all-time-low` if ITAD hist-low **or** `salePrice <= low * 1.001`
  - `good` if within 10% of the best ever
  - `meh` if cheaper has been seen
  - `''` (no badge) when there's no history yet — avoids lying on first sight
  - `IsSuspectDiscount`: conservative inflated-MSRP flag.
- **Save paths:** `SaveDealWithVerdict` records the price, computes the verdict,
  and persists the three new fields.
- **API/UI:** verdict fields on the `Deal` struct + `QueryDeals` SELECT; a
  `great=1` filter (`verdict IN ('all-time-low','good')`) and a "Best deals"
  sort; badges `🔥 All-time low` / `✓ Good price` / `⚠ Check price`, a
  "Seen as low as $X" line, and a "Great deals only" toggle.
- **Tests:** `verdict_test.go` boundary cases + the descending-price transition
  `'' → meh → good → all-time-low`.

---

## Phase 2 — Watchlist 2.0  ✅

**Goal:** Extend Pastel's stickiest feature beyond exact game-title matching.

### Built (commit `49b6daf`)

**Predicate watches** — `!watch elden ring under 30`, `!watch laptop over 40% off`.
- `internal/watchlist/parse.go` `ParseWatch(args) WatchSpec` extracts trailing
  `under/below/< N`, `over N% off` / `N% off`, and `category:`/`keyword:` tokens;
  the remainder is the match label. Forgiving: unrecognized tokens stay in the label.
- New `watchlist` columns `max_price REAL`, `min_discount INTEGER` (0 = unconstrained).
- Enforced in `FindMatchingUsers(MatchDeal)`: category → substring title match →
  price cap → discount floor. Free deals satisfy any price cap; an unknown price
  (0) with a cap set is skipped rather than risk a false alert.

**Keyword + category watches** — `!watch category:clothing nike`.
- New `category TEXT` column scopes a watch to one deal category (`''` = any).
- **RSS/web deals now trigger watchlist DMs for the first time.** They are never
  posted to Matrix, so `posted_deals` is reused purely as a once-per-deal
  notification ledger (`notifyWebDealWatchers` in `cmd/pastel/main.go`): a web
  deal notifies only the first time its `dedup_id` is seen.
- **Deploy-safety:** the first web-deal scan on an existing DB would otherwise
  DM a backlog of already-live deals to existing watchers. A `web_deals_seeded`
  config flag makes the first scan record current deals *without* notifying —
  mirroring how the game sources seed via `populateInitialState`.

**Instant vs daily digest** — `!digest on|off`.
- New `user_prefs(user_id, notify_mode)` table; `instant` (default) or `daily`.
- Daily matches accumulate in a restart-safe `pending_digest` table
  (`QueueDigest`/`TakeDigest` in `internal/watchlist/digest.go`); a 24h ticker
  (`flushWatchlistDigests`) DMs one summary and clears the queue. Old rows are
  pruned after 7 days as a safety valve.

**Behavioral notes**
- Re-watching a label **refines** its predicates/category/expiry in place but
  keeps the original display name (preserves casing; e.g. re-adding
  "hollow knight" doesn't lowercase the stored "Hollow Knight").
- The web watchlist form shares `ParseWatch`, so `laptop under 500` works there too.
- `notifyWatchlistMatches`/`notifyWatchlistFreeMatches` were unified into one
  `notifyWatchlist(MatchDeal, url, price)` that honors each user's notify mode.

**Tests:** parser table-test (`parse_test.go`), predicate/category matching +
digest queue (`store_test.go`), command-handler replies with a fake DM sender
(`commands_test.go`), and a prod-DB migration guard
(`migrate_watchlist_test.go`) confirming columns/tables are added idempotently to
a pre-Phase-2 `watchlist`.

**Not done in-session:** a live Matrix DM smoke test (needs the running bot) —
do this on next deploy.

---

## Phase 3 — Community layer  ✅

**Goal:** Parodia-only signal no public aggregator can copy. Required new Matrix
plumbing.

### Built (2026-06-28)

- **Deal → event_id mapping (the prerequisite that didn't exist).**
  `SendDealInThread` now returns the posted message's event ID; all three posting
  sites (cheapshark/itad/epic in `main.go`) call `db.SetDealEventID(dedupID,
  eventID)` after posting. Stored as additive `deals.event_id TEXT` (indexed).
  `SaveDeal`'s upsert never touches `event_id`, so the mapping survives price
  refreshes.
- **Reaction ingestion.** `RegisterReactionHandler` (`event.EventReaction`) +
  `RegisterRedactionHandler` (`event.EventRedaction`, for un-react) in
  `internal/matrix/client.go`, wired in `main.go` scoped to the deals room.
  Storage in `internal/database/reactions.go`: `deal_reactions(reaction_event_id
  PK, target_event_id, user_id)`; `AddReaction`/`RemoveReaction` recompute
  `deals.reaction_count` as `COUNT(DISTINCT user_id)`. Idempotent by reaction
  event ID, so a restart replaying recent timeline never double-counts; reactions
  to non-deal events are ignored. Reactions are intentionally **not** start-time
  filtered (members react to old deals) — dedup makes that safe.
- **Community ranking + Heat.** `reaction_count` column + `hot` sort
  (`reaction_count / (age_in_days + 1)`, using core `julianday()` — no math
  extension). Surfaced as a **🔥 Heat (most loved)** option in the sort dropdown
  (cleaner than a pseudo-category pill in the data-driven catnav) plus a `🔥 N`
  chip on cards. "X members watching" comes from a `watcher_count` correlated
  subquery (`watchlist` rows whose normalized name == the deal's) rendered as a
  `👀 N watching` chip.
- **Weekly room digest.** `postWeeklyHeatDigest` (`TopDealsSince(7, 5)` +
  `FormatWeeklyHeatDigest`) posts "🔥 Hottest deals this week" on a 7-day ticker;
  no-op on a quiet week. First tick is a week out, so a restart never re-posts.
- **Pruning.** `PruneReactions` drops reactions whose deal aged out, alongside the
  existing pruners.
- **Tests:** `reactions_test.go` (distinct-user counting, replay idempotency,
  multi-emoji, redaction, `TopDealsSince` + `hot` ordering); Phase-3 columns added
  to the existing-DB migration guard.

**Not done in-session:** live Matrix smoke test (post a deal, react from a second
account, confirm the count increments and Heat reorders) — needs the running bot;
do on next deploy, same as the Phase 2 DM smoke test.

## Phase 4 — Deal images  ✅

**Goal:** Give the gallery actual item imagery. Cards were text-only because no
source struct carried an image. This phase threaded an image URL from each
source to the card and degrades to a plain text-only card where there isn't one.

### Built (2026-06-28)

- **Per-source extraction.** Each source struct now carries `ImageURL`, populated
  at fetch time:
  - **CheapShark** — maps the `thumb` field from the raw JSON (`cheapshark.go`),
    in both the deals feed and the search path.
  - **RSS (DealNews, Slickdeals)** — `rss.go` `imageFromItem()` picks, in order,
    `media:content` → `media:thumbnail` → an image `enclosure` → the first
    `<img src>` in `description`/`content:encoded`. `rssItem` gained `rssMedia`/
    `rssEnclosure` fields matched by local name; non-image enclosures and
    non-http(s) srcs are rejected (`isImageURL`/`isHTTPURL`).
  - **Epic** — `epicImage()` prefers `OfferImageWide` → `DieselStoreFrontWide` →
    `Thumbnail` → any http(s) `keyImages` entry.
  - **ITAD** — left blank (the v2 deals API exposes no asset in our struct); the
    text-only card covers it.
- **Schema + plumbing.** Additive `deals.image_url TEXT NOT NULL DEFAULT ''`
  column (idempotent `addColumnIfMissing` + in the fresh `CREATE TABLE`);
  `ImageURL` added to the `database.Deal` struct, `dealColumns` (so both
  `QueryDeals` and `TopDealsSince` select it), `SaveDeal`'s INSERT/UPDATE, and
  every `save*Deals` mapper in `persist.go`.
- **Frontend.** A lazy (`loading="lazy" decoding="async"`) full-bleed thumbnail
  at the top of the card (`cardHTML` in `app.js`), guarded by the existing
  `safeURL()` (http(s) only — covers `src` as well as the link). **No mascot
  fallback:** an image-less deal renders text-only, and a failed/404 load
  (`onerror="this.closest('.thumb').remove()"`) removes the figure so the grid
  never shows a broken-image icon. CSS `.thumb` (`style.css`): negative margins
  to bleed past the card padding (the card's `overflow:hidden`+radius rounds the
  top corners), fixed 16/9 `aspect-ratio`, `object-fit:cover`, gentle hover zoom.
- **Tests:** `rss_test.go` `TestImageFromItem` covers the precedence chain,
  non-image-enclosure skip, entity-decoded `<img>`, `data:` rejection, and the
  no-image case; the existing-DB migration guard asserts `image_url` is added and
  backfills to `''`.

**Verified:** rendered the gallery with a seeded DB — a real Steam header and a
local image render as rounded 16:9 thumbnails, an image-less deal stays text-only,
and a deliberately-broken URL self-removes via `onerror` (no broken icon).

## Phase 5 — Coverage & reach  ✅

**Goal:** More of what Parodia actually buys, plus app-like delivery.

### Built (2026-06-28)

- **🎁 Free super-tab.** A synthetic catnav selection (`FREE_TAB` sentinel in
  `app.js`) that cross-cuts every vertical: it maps to `free=1` (no category)
  instead of a real category, reusing the existing `is_free` column + `FreeOnly`
  filter. Rendered up front with a minty accent (`.free-pill`); the sidebar "Free
  only" checkbox remains an independent route to the same param. No backend change
  — the filter already existed from Phase 1/2.
- **New sources — DealNews verticals, *not* Slickdeals.** Probing showed
  Slickdeals's RSS **ignores** its category params (`fcid`/`forumid` all return the
  same frontpage), and Woot/GOG/Humble no longer expose usable RSS. DealNews
  category feeds *are* cleanly segmented, so coverage was expanded by adding six
  feeds to `dealNewsFeeds` (Tools-Hardware→`tools`, Health-Beauty→`beauty`,
  Automotive→`auto`, Babies-Kids→`kids`, Office-School-Supplies→`office`,
  Pet-Supplies→`pets`). New verticals appear in the data-driven catnav
  automatically; `CATEGORY_META` got icons/labels for each. **Reddit was
  explicitly dropped** (datacenter-IP throttling + would need an Atom parser).
- **PWA.** `manifest.webmanifest` (standalone, theme `#ffe1f1`, maskable + any
  icons generated from the mascot at 192/512), `sw.js` service worker (app-shell
  precache + stale-while-revalidate for static, network-first navigations, never
  caches `/api/` or `/auth/`), linked from `index.html` and registered in
  `app.js`. `.webmanifest` MIME registered in `web` `init()`.
- **Web Push (RFC 8291 + VAPID, in-repo, zero new deps).** `internal/webpush`
  implements VAPID ES256 JWTs and `aes128gcm` message encryption using only
  stdlib `crypto/ecdh`/`ecdsa` + `x/crypto/hkdf` (already in the module graph) —
  no third-party push library. The VAPID keypair is generated once and persisted
  in `config` (`vapid_private_key`). Subscriptions live in a new
  `push_subscriptions(endpoint PK, user_id, p256dh, auth)` table keyed by the
  Matrix mxid — the *same* id the watchlist and `FindMatchingUsers` use, so a
  watch match maps straight to a browser. API: `GET /api/push/config` (public
  VAPID key), auth-gated `POST /api/push/{subscribe,unsubscribe}`. Delivery hooks
  into `notifyWatchlist` (the single choke point all match sites flow through) via
  a process-wide `pushOut`; it fires on every match independent of Matrix notify
  mode, and prunes endpoints the push service reports Gone (404/410). Frontend
  toggle (🔔) in the watchlist drawer, shown only when signed in + push supported.
- **Tests:** `webpush_test.go` — RFC 8291 round-trip via an *independently-coded*
  receiver-side decrypt (catches any key-schedule/info-string/header divergence),
  auth-secret-is-bound negative test, VAPID JWT sign+verify, and an httptest Send
  asserting the posted body decrypts. The existing-DB migration guard now also
  exercises `push_subscriptions` (save/list/delete) on a pre-Phase-5 database.

**Verified:** rendered a seeded gallery — the 🎁 Free pill + all six new vertical
pills render with their icons, `free=1` returns only free items across categories,
and the manifest/`sw.js`/icons all serve (manifest as `application/manifest+json`)
with the service worker registering and no console errors.

**Not done in-session (needs real infra, like prior phases' smoke tests):** an
end-to-end push delivery through a live browser push service (FCM/Mozilla) and the
install-prompt — both require a real browser over HTTPS at deploy. The crypto is
unit-validated; interop is the deploy check.

---

## Build order & checkpoints

1. ✅ Phase 1 — implemented, tested, committed.
2. ✅ Phase 2 — implemented, tested, committed.
3. ✅ Phase 3 — implemented & tested; live Matrix smoke test on next deploy.
4. ✅ Phase 4 (images) — per-source extraction + lazy card thumbnails, verified.
5. ✅ Phase 5 — free super-tab + DealNews verticals + PWA + in-repo Web Push;
   verified locally, live push delivery deferred to deploy.

Each phase is independently shippable and leaves the service working.
