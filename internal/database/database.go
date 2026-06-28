package database

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/prosolis/Pastel/internal/normalize"
)

type DB struct {
	db *sqlx.DB
}

// Bool is a SQLite-friendly boolean. SQLite stores booleans as integers, and
// the database/sql layer will not scan an int64 into a plain *bool, so we wrap
// it. It marshals to a real JSON boolean for the web API.
type Bool bool

func (b *Bool) Scan(v any) error {
	switch t := v.(type) {
	case nil:
		*b = false
	case bool:
		*b = Bool(t)
	case int64:
		*b = t != 0
	case float64:
		*b = t != 0
	case []byte:
		*b = Bool(truthy(string(t)))
	case string:
		*b = Bool(truthy(t))
	default:
		return fmt.Errorf("cannot scan %T into Bool", v)
	}
	return nil
}

// truthy interprets a textual boolean from SQLite (which may surface a flag
// column as text depending on its declared affinity).
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "t", "yes", "y":
		return true
	}
	return false
}

func (b Bool) Value() (driver.Value, error) {
	if b {
		return int64(1), nil
	}
	return int64(0), nil
}

func Open(path string) (*DB, error) {
	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	// The bot loop and the in-process web server share this single handle.
	// SQLite allows only one writer at a time; capping the pool to one
	// connection serializes access so concurrent writes/reads don't fail with
	// SQLITE_BUSY (and so the busy_timeout/PRAGMA state can't vary per conn).
	db.SetMaxOpenConns(1)
	d := &DB{db: db}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

// RawDB exposes the underlying sqlx.DB for packages that need direct access.
func (d *DB) RawDB() *sqlx.DB {
	return d.db
}

func (d *DB) migrate() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS posted_deals (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			title TEXT NOT NULL,
			posted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT
		);
		CREATE TABLE IF NOT EXISTS watchlist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			game_name TEXT NOT NULL,
			game_name_normalized TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL,
			expiry_warned INTEGER DEFAULT 0,
			UNIQUE(user_id, game_name_normalized)
		);
		CREATE INDEX IF NOT EXISTS idx_watchlist_normalized ON watchlist(game_name_normalized);
		CREATE INDEX IF NOT EXISTS idx_watchlist_expires ON watchlist(expires_at);

		-- user_prefs holds per-user notification settings. notify_mode is
		-- 'instant' (DM each match immediately, the default) or 'daily' (collect
		-- matches in pending_digest and DM one digest per day).
		CREATE TABLE IF NOT EXISTS user_prefs (
			user_id     TEXT PRIMARY KEY,
			notify_mode TEXT NOT NULL DEFAULT 'instant',
			updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		-- pending_digest accumulates watchlist matches for users in 'daily' mode.
		-- It is a table (not in-memory) so a restart never drops queued matches.
		CREATE TABLE IF NOT EXISTS pending_digest (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id   TEXT NOT NULL,
			label     TEXT NOT NULL,
			title     TEXT NOT NULL,
			url       TEXT NOT NULL,
			price     TEXT,
			discount  INTEGER DEFAULT 0,
			is_free   INTEGER DEFAULT 0,
			queued_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_pending_digest_user ON pending_digest(user_id);

		-- deals stores the full data of every deal the bot has seen so the web
		-- interface has something rich to browse. posted_deals remains the source
		-- of truth for dedup/posting; this table is a superset used for display.
		CREATE TABLE IF NOT EXISTS deals (
			dedup_id         TEXT PRIMARY KEY,
			source           TEXT NOT NULL,
			category         TEXT NOT NULL DEFAULT 'games',
			kind             TEXT NOT NULL,
			title            TEXT NOT NULL,
			title_normalized TEXT NOT NULL,
			store            TEXT,
			sale_price       REAL,
			normal_price     REAL,
			discount         INTEGER,
			rating           REAL,
			url              TEXT,
			is_hist_low      INTEGER DEFAULT 0,
			is_free          INTEGER DEFAULT 0,
			upcoming         INTEGER DEFAULT 0,
			expires_at       TIMESTAMP,
			posted_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_deals_posted ON deals(posted_at);
		CREATE INDEX IF NOT EXISTS idx_deals_source ON deals(source);
		CREATE INDEX IF NOT EXISTS idx_deals_title_norm ON deals(title_normalized);

		-- price_history records every (non-free) price Pastel has observed for a
		-- product, keyed by a stable product key (see PriceKey). It is the basis
		-- for the trust "verdict": dedup_id changes when the discount changes, so
		-- per-row history can't accumulate — this table can.
		CREATE TABLE IF NOT EXISTS price_history (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			price_key  TEXT NOT NULL,
			source     TEXT NOT NULL,
			sale_price REAL NOT NULL,
			seen_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_price_history_key ON price_history(price_key, seen_at);

		-- web_sessions backs OIDC-authenticated web sessions.
		CREATE TABLE IF NOT EXISTS web_sessions (
			token        TEXT PRIMARY KEY,
			user_id      TEXT NOT NULL,
			display_name TEXT,
			created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			expires_at   TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_web_sessions_expires ON web_sessions(expires_at);
	`)
	if err != nil {
		return err
	}

	// Additive column migrations for databases created before a column existed
	// (the CREATE TABLE statements above only take effect on a fresh install).
	// Each step is idempotent so migrate() is safe to run on every startup.
	if err := d.addColumnIfMissing("deals", "category", "category TEXT NOT NULL DEFAULT 'games'"); err != nil {
		return err
	}
	if _, err := d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_deals_category ON deals(category)`); err != nil {
		return err
	}
	// Phase 1: price-verdict columns. verdict is the trust badge bucket,
	// price_low is the lowest sale price Pastel has ever observed for the
	// product, price_suspect flags a likely inflated discount.
	if err := d.addColumnIfMissing("deals", "verdict", "verdict TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := d.addColumnIfMissing("deals", "price_low", "price_low REAL"); err != nil {
		return err
	}
	if err := d.addColumnIfMissing("deals", "price_suspect", "price_suspect INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	// Phase 2: predicate/category watch columns. max_price and min_discount are
	// the trailing conditions from "!watch X under 30" / "over 40% off" (0 means
	// unconstrained); category scopes a watch to one deal category ('' = any).
	if err := d.addColumnIfMissing("watchlist", "max_price", "max_price REAL NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := d.addColumnIfMissing("watchlist", "min_discount", "min_discount INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := d.addColumnIfMissing("watchlist", "category", "category TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

// addColumnIfMissing adds a column to an existing table when it is absent, so
// schema additions reach databases created before the column existed. SQLite's
// ALTER TABLE ... ADD COLUMN errors if the column is already there, so we guard
// on pragma_table_info first.
func (d *DB) addColumnIfMissing(table, column, ddl string) error {
	var n int
	if err := d.db.Get(&n, "SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := d.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + ddl)
	return err
}

// IsPosted checks if a deal has already been posted.
func (d *DB) IsPosted(id string) (bool, error) {
	var count int
	err := d.db.Get(&count, "SELECT COUNT(1) FROM posted_deals WHERE id = ?", id)
	return count > 0, err
}

// MarkPosted records a deal as posted.
func (d *DB) MarkPosted(id, source, title string) error {
	_, err := d.db.Exec(
		"INSERT OR IGNORE INTO posted_deals (id, source, title) VALUES (?, ?, ?)",
		id, source, title,
	)
	return err
}

// IsFirstRunDone checks if the initial population has been completed.
func (d *DB) IsFirstRunDone() (bool, error) {
	var val string
	err := d.db.Get(&val, "SELECT value FROM config WHERE key = 'first_run_done'")
	if err != nil {
		return false, nil // not found = not done
	}
	return val == "true", nil
}

// SetFirstRunDone marks the initial population as complete.
func (d *DB) SetFirstRunDone() error {
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO config (key, value) VALUES ('first_run_done', 'true')",
	)
	return err
}

// GetConfig retrieves a value from the config table.
func (d *DB) GetConfig(key string) (string, error) {
	var val string
	err := d.db.Get(&val, "SELECT value FROM config WHERE key = ?", key)
	return val, err
}

// SetConfig sets a value in the config table.
func (d *DB) SetConfig(key, value string) error {
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO config (key, value) VALUES (?, ?)",
		key, value,
	)
	return err
}

// PruneOldDeals removes deals older than the given number of days.
func (d *DB) PruneOldDeals(days int) error {
	cutoff := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	_, err := d.db.Exec("DELETE FROM posted_deals WHERE posted_at < ?", cutoff)
	return err
}

// Deal is the full, display-ready record of a deal for the web interface.
type Deal struct {
	DedupID     string     `db:"dedup_id" json:"id"`
	Source      string     `db:"source" json:"source"`
	Category    string     `db:"category" json:"category"` // games | music | clothing | ...
	Kind        string     `db:"kind" json:"kind"`         // game | dlc | free | deal
	Title       string     `db:"title" json:"title"`
	TitleNorm   string     `db:"title_normalized" json:"-"`
	Store       string     `db:"store" json:"store"`
	SalePrice   float64    `db:"sale_price" json:"salePrice"`
	NormalPrice float64    `db:"normal_price" json:"normalPrice"`
	Discount    int        `db:"discount" json:"discount"`
	Rating      float64    `db:"rating" json:"rating"`
	URL         string     `db:"url" json:"url"`
	IsHistLow   Bool       `db:"is_hist_low" json:"isHistLow"`
	IsFree      Bool       `db:"is_free" json:"isFree"`
	Upcoming    Bool       `db:"upcoming" json:"upcoming"`
	ExpiresAt   *time.Time `db:"expires_at" json:"expiresAt,omitempty"`
	PostedAt    time.Time  `db:"posted_at" json:"postedAt"`
	// Phase 1 trust signals. Verdict is "" until Pastel has price history.
	Verdict      string  `db:"verdict" json:"verdict,omitempty"`              // all-time-low | good | meh | ""
	PriceLow     float64 `db:"price_low" json:"priceLow,omitempty"`          // lowest observed sale price
	PriceSuspect Bool    `db:"price_suspect" json:"priceSuspect,omitempty"`  // likely inflated discount
}

// PriceKey is the stable product identity used to accumulate price history.
// dedup_id is unsuitable because it embeds the discount/timestamp and so
// changes whenever the price moves; the normalized title (plus category, to
// avoid cross-category collisions) is stable across re-sees of the same item.
func PriceKey(category, titleNorm string) string {
	if category == "" {
		category = "games"
	}
	return category + "|" + titleNorm
}

// SaveDeal upserts a deal's full data. The first insert sets posted_at; later
// upserts refresh the mutable fields (price, discount, flags) and updated_at.
func (d *DB) SaveDeal(deal Deal) error {
	if deal.Category == "" {
		deal.Category = "games"
	}
	_, err := d.db.NamedExec(`
		INSERT INTO deals (
			dedup_id, source, category, kind, title, title_normalized, store,
			sale_price, normal_price, discount, rating, url,
			is_hist_low, is_free, upcoming, expires_at,
			verdict, price_low, price_suspect
		) VALUES (
			:dedup_id, :source, :category, :kind, :title, :title_normalized, :store,
			:sale_price, :normal_price, :discount, :rating, :url,
			:is_hist_low, :is_free, :upcoming, :expires_at,
			:verdict, :price_low, :price_suspect
		)
		ON CONFLICT(dedup_id) DO UPDATE SET
			store         = excluded.store,
			sale_price    = excluded.sale_price,
			normal_price  = excluded.normal_price,
			discount      = excluded.discount,
			rating        = excluded.rating,
			url           = excluded.url,
			is_hist_low   = excluded.is_hist_low,
			is_free       = excluded.is_free,
			upcoming      = excluded.upcoming,
			expires_at    = excluded.expires_at,
			verdict       = excluded.verdict,
			price_low     = excluded.price_low,
			price_suspect = excluded.price_suspect,
			updated_at    = CURRENT_TIMESTAMP
	`, &deal)
	return err
}

// RecordPrice appends a price observation for a product key. Free/zero prices
// are ignored so giveaways don't poison the historical low. Call this before
// upserting a deal so LowestPrice reflects the current sighting.
func (d *DB) RecordPrice(priceKey, source string, salePrice float64) error {
	if priceKey == "" || salePrice <= 0 {
		return nil
	}
	_, err := d.db.Exec(
		"INSERT INTO price_history (price_key, source, sale_price) VALUES (?, ?, ?)",
		priceKey, source, salePrice,
	)
	return err
}

// LowestPrice returns the lowest observed sale price for a product key and
// whether any history exists. Used to compute the trust verdict.
func (d *DB) LowestPrice(priceKey string) (float64, bool) {
	var low sql.NullFloat64
	if err := d.db.Get(&low, "SELECT MIN(sale_price) FROM price_history WHERE price_key = ?", priceKey); err != nil {
		return 0, false
	}
	return low.Float64, low.Valid
}

// MedianPrice returns the median observed sale price for a product key and
// whether any history exists, used to detect inflated "normal" prices.
func (d *DB) MedianPrice(priceKey string) (float64, bool) {
	var prices []float64
	if err := d.db.Select(&prices, "SELECT sale_price FROM price_history WHERE price_key = ? ORDER BY sale_price", priceKey); err != nil || len(prices) == 0 {
		return 0, false
	}
	n := len(prices)
	if n%2 == 1 {
		return prices[n/2], true
	}
	return (prices[n/2-1] + prices[n/2]) / 2, true
}

// Deal pagination bounds. The web API clamps client-supplied page sizes to
// this range so a missing or out-of-range limit falls back to the default.
const (
	DefaultDealLimit = 48
	MaxDealLimit     = 200
)

// ClampDealLimit normalizes a client-supplied page size to the supported range,
// defaulting unset (<=0) or oversized values to DefaultDealLimit. Callers use
// it to report the effective limit they actually applied.
func ClampDealLimit(limit int) int {
	if limit <= 0 || limit > MaxDealLimit {
		return DefaultDealLimit
	}
	return limit
}

// DealFilter describes the query the web interface wants to run against deals.
type DealFilter struct {
	Query       string
	Categories  []string
	Sources     []string
	Stores      []string
	Kinds       []string
	MinDiscount int
	MaxPrice    float64 // 0 = no limit
	HistLowOnly bool
	FreeOnly    bool
	GreatOnly   bool   // only verdict all-time-low or good
	Sort        string // newest | discount | price_asc | price_desc | rating | verdict
	Limit       int
	Offset      int
}

// QueryDeals returns deals matching the filter plus the total match count
// (ignoring limit/offset) for pagination.
func (d *DB) QueryDeals(f DealFilter) ([]Deal, int, error) {
	var where []string
	var args []any

	if q := strings.TrimSpace(f.Query); q != "" {
		where = append(where, "title_normalized LIKE ?")
		args = append(args, "%"+normalize.Text(q)+"%")
	}
	if len(f.Categories) > 0 {
		where = append(where, "category IN ("+placeholders(len(f.Categories))+")")
		for _, c := range f.Categories {
			args = append(args, c)
		}
	}
	if len(f.Sources) > 0 {
		where = append(where, "source IN ("+placeholders(len(f.Sources))+")")
		for _, s := range f.Sources {
			args = append(args, s)
		}
	}
	if len(f.Stores) > 0 {
		where = append(where, "store IN ("+placeholders(len(f.Stores))+")")
		for _, s := range f.Stores {
			args = append(args, s)
		}
	}
	if len(f.Kinds) > 0 {
		where = append(where, "kind IN ("+placeholders(len(f.Kinds))+")")
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if f.MinDiscount > 0 {
		where = append(where, "discount >= ?")
		args = append(args, f.MinDiscount)
	}
	if f.MaxPrice > 0 {
		where = append(where, "sale_price <= ?")
		args = append(args, f.MaxPrice)
	}
	if f.HistLowOnly {
		where = append(where, "is_hist_low = 1")
	}
	if f.FreeOnly {
		where = append(where, "is_free = 1")
	}
	if f.GreatOnly {
		where = append(where, "verdict IN ('all-time-low', 'good')")
	}

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := d.db.Get(&total, "SELECT COUNT(1) FROM deals"+clause, args...); err != nil {
		return nil, 0, err
	}

	order := orderClause(f.Sort)
	limit := ClampDealLimit(f.Limit)
	query := "SELECT dedup_id, source, category, kind, title, title_normalized, store, " +
		"sale_price, normal_price, discount, rating, url, is_hist_low, is_free, " +
		"upcoming, expires_at, posted_at, verdict, price_low, price_suspect FROM deals" + clause + order + " LIMIT ? OFFSET ?"
	args = append(args, limit, f.Offset)

	var deals []Deal
	if err := d.db.Select(&deals, query, args...); err != nil {
		return nil, 0, err
	}
	return deals, total, nil
}

// DealFacets returns the distinct categories, sources, and stores currently
// present, for building the filter/navigation UI.
func (d *DB) DealFacets() (categories []string, sources []string, stores []string, err error) {
	if err = d.db.Select(&categories, "SELECT DISTINCT category FROM deals WHERE category IS NOT NULL AND category != '' ORDER BY category"); err != nil {
		return nil, nil, nil, err
	}
	if err = d.db.Select(&sources, "SELECT DISTINCT source FROM deals ORDER BY source"); err != nil {
		return nil, nil, nil, err
	}
	if err = d.db.Select(&stores, "SELECT DISTINCT store FROM deals WHERE store IS NOT NULL AND store != '' ORDER BY store"); err != nil {
		return nil, nil, nil, err
	}
	return categories, sources, stores, nil
}

// WebSession is an authenticated web session backed by the web_sessions table.
type WebSession struct {
	Token       string    `db:"token"`
	UserID      string    `db:"user_id"`
	DisplayName string    `db:"display_name"`
	CreatedAt   time.Time `db:"created_at"`
	ExpiresAt   time.Time `db:"expires_at"`
}

// CreateSession stores a new web session.
func (d *DB) CreateSession(token, userID, displayName string, expiresAt time.Time) error {
	_, err := d.db.Exec(
		`INSERT INTO web_sessions (token, user_id, display_name, expires_at) VALUES (?, ?, ?, ?)`,
		token, userID, displayName, expiresAt.UTC().Format(time.RFC3339),
	)
	return err
}

// GetSession returns the session for a token if it exists and has not expired.
// Returns (nil, nil) when there is no valid session for the token.
func (d *DB) GetSession(token string) (*WebSession, error) {
	// Compare against a Go-formatted RFC3339 timestamp rather than SQLite's
	// CURRENT_TIMESTAMP: expires_at is stored as RFC3339 (with a "T"), which
	// does not order correctly against CURRENT_TIMESTAMP's "YYYY-MM-DD HH:MM:SS".
	now := time.Now().UTC().Format(time.RFC3339)
	var s WebSession
	err := d.db.Get(&s,
		`SELECT token, user_id, display_name, created_at, expires_at
		 FROM web_sessions WHERE token = ? AND expires_at > ?`,
		token, now,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// DeleteSession removes a session by token.
func (d *DB) DeleteSession(token string) error {
	_, err := d.db.Exec("DELETE FROM web_sessions WHERE token = ?", token)
	return err
}

// PruneSessions removes expired web sessions.
func (d *DB) PruneSessions() error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.Exec("DELETE FROM web_sessions WHERE expires_at <= ?", now)
	return err
}

// PruneDealsTable removes display deals older than the given number of days.
func (d *DB) PruneDealsTable(days int) error {
	cutoff := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	_, err := d.db.Exec("DELETE FROM deals WHERE posted_at < ?", cutoff)
	return err
}

// PrunePriceHistory removes price observations older than the given number of
// days. Keep a longer window than the deals table (~180d) so "all-time low"
// stays meaningful while remaining bounded.
func (d *DB) PrunePriceHistory(days int) error {
	cutoff := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	_, err := d.db.Exec("DELETE FROM price_history WHERE seen_at < ?", cutoff)
	return err
}

func orderClause(sort string) string {
	switch sort {
	case "discount":
		return " ORDER BY discount DESC, posted_at DESC"
	case "price_asc":
		return " ORDER BY sale_price ASC, posted_at DESC"
	case "price_desc":
		return " ORDER BY sale_price DESC, posted_at DESC"
	case "rating":
		return " ORDER BY rating DESC, posted_at DESC"
	case "verdict":
		// Best deals first: all-time-low, then good, then everything else,
		// newest within each bucket.
		return " ORDER BY CASE verdict WHEN 'all-time-low' THEN 0 WHEN 'good' THEN 1 ELSE 2 END, posted_at DESC"
	default: // newest
		return " ORDER BY posted_at DESC"
	}
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
