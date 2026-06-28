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
| 3 | Community layer | ⬜ Next — Matrix reaction infra (highest risk) |
| 4 | Coverage & reach | ⬜ Planned |

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

## Phase 3 — Community layer  ⬜ *(tighten before build — largest surface)*

**Goal:** Parodia-only signal no public aggregator can copy. Requires new Matrix
plumbing.

- **Deal → event_id mapping (prerequisite, does not exist today).** When posting
  via `SendDealInThread`, capture the returned event ID and store it (add
  `event_id TEXT` to `deals`, or a `deal_events` table). This is the join key for
  reactions.
- **Reaction ingestion.** Register an `event.EventReaction` handler alongside the
  message handler. On an `m.reaction` whose `m.relates_to` target is a known deal
  event, increment a `reaction_count` (optionally per-user to dedup).
- **Community ranking + Heat tab.** Add `reaction_count INTEGER`; new `hot` sort
  (recency-decayed reaction score). Frontend `🔥 Heat` tab (data-driven nav
  already supports new tabs) and an "X members watching" line on game deals
  (count from `watchlist` by normalized name).
- **Weekly room digest.** Reuse the ticker pattern to post a "Top 5 this week"
  message to the deals room.
- **Verify:** post a test deal, react from a second account, confirm the count
  increments and the Heat tab reorders.

## Phase 4 — Coverage & reach  ⬜ *(tighten before build)*

**Goal:** More of what Parodia actually buys, plus app-like delivery.

- **Free-stuff super-tab.** Tag free items so the data-driven nav renders a
  `🎁 Free` pill aggregating Epic + future GOG/Prime/Steam free weekends.
- **New sources** following the RSS pattern (`FetchXxx() ([]WebDeal, error)` →
  register in `DEAL_SOURCES` → call from `checkWebDeals`): prioritize
  `/r/buildapcsales`, Humble, GOG, Woot. Amazon needs a Keepa-style price source —
  defer/scope separately.
- **PWA + Web Push.** Manifest + service worker in the embedded static set; use
  the price-verdict + watchlist signals to push "all-time low on your watch"
  without a Matrix DM. Largest net-new infra — scope as its own sub-phase.
- **Verify:** each source behind its `DEAL_SOURCES` flag with a fetch smoke test;
  PWA installability via devtools.

---

## Build order & checkpoints

1. ✅ Phase 1 — implemented, tested, committed.
2. ✅ Phase 2 — implemented, tested, committed.
3. Phase 3 — tighten (Matrix reaction infra is the risk) → build → verify → commit.
4. Phase 4 — sources incrementally; PWA as its own sub-phase.

Each phase is independently shippable and leaves the service working.
