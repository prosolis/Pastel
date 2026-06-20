package database

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
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
		*b = len(t) == 1 && t[0] == '1'
	default:
		return fmt.Errorf("cannot scan %T into Bool", v)
	}
	return nil
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

		-- deals stores the full data of every deal the bot has seen so the web
		-- interface has something rich to browse. posted_deals remains the source
		-- of truth for dedup/posting; this table is a superset used for display.
		CREATE TABLE IF NOT EXISTS deals (
			dedup_id         TEXT PRIMARY KEY,
			source           TEXT NOT NULL,
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
	Kind        string     `db:"kind" json:"kind"` // game | dlc | free
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
}

// SaveDeal upserts a deal's full data. The first insert sets posted_at; later
// upserts refresh the mutable fields (price, discount, flags) and updated_at.
func (d *DB) SaveDeal(deal Deal) error {
	_, err := d.db.NamedExec(`
		INSERT INTO deals (
			dedup_id, source, kind, title, title_normalized, store,
			sale_price, normal_price, discount, rating, url,
			is_hist_low, is_free, upcoming, expires_at
		) VALUES (
			:dedup_id, :source, :kind, :title, :title_normalized, :store,
			:sale_price, :normal_price, :discount, :rating, :url,
			:is_hist_low, :is_free, :upcoming, :expires_at
		)
		ON CONFLICT(dedup_id) DO UPDATE SET
			store        = excluded.store,
			sale_price   = excluded.sale_price,
			normal_price = excluded.normal_price,
			discount     = excluded.discount,
			rating       = excluded.rating,
			url          = excluded.url,
			is_hist_low  = excluded.is_hist_low,
			is_free      = excluded.is_free,
			upcoming     = excluded.upcoming,
			expires_at   = excluded.expires_at,
			updated_at   = CURRENT_TIMESTAMP
	`, &deal)
	return err
}

// DealFilter describes the query the web interface wants to run against deals.
type DealFilter struct {
	Query       string
	Sources     []string
	Stores      []string
	Kinds       []string
	MinDiscount int
	MaxPrice    float64 // 0 = no limit
	HistLowOnly bool
	FreeOnly    bool
	Sort        string // newest | discount | price_asc | price_desc | rating
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
		args = append(args, "%"+normalizeQuery(q)+"%")
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

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := d.db.Get(&total, "SELECT COUNT(1) FROM deals"+clause, args...); err != nil {
		return nil, 0, err
	}

	order := orderClause(f.Sort)
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 48
	}
	query := "SELECT dedup_id, source, kind, title, title_normalized, store, " +
		"sale_price, normal_price, discount, rating, url, is_hist_low, is_free, " +
		"upcoming, expires_at, posted_at FROM deals" + clause + order + " LIMIT ? OFFSET ?"
	args = append(args, limit, f.Offset)

	var deals []Deal
	if err := d.db.Select(&deals, query, args...); err != nil {
		return nil, 0, err
	}
	return deals, total, nil
}

// DealFacets returns the distinct sources and stores currently present, for
// building the filter UI.
func (d *DB) DealFacets() (sources []string, stores []string, err error) {
	if err = d.db.Select(&sources, "SELECT DISTINCT source FROM deals ORDER BY source"); err != nil {
		return nil, nil, err
	}
	if err = d.db.Select(&stores, "SELECT DISTINCT store FROM deals WHERE store IS NOT NULL AND store != '' ORDER BY store"); err != nil {
		return nil, nil, err
	}
	return sources, stores, nil
}

// PruneDealsTable removes display deals older than the given number of days.
func (d *DB) PruneDealsTable(days int) error {
	cutoff := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	_, err := d.db.Exec("DELETE FROM deals WHERE posted_at < ?", cutoff)
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

// normalizeQuery lowercases, strips non-alphanumeric characters (keeping
// spaces), and collapses whitespace so a search term matches the stored
// title_normalized. It mirrors watchlist.Normalize; kept local to avoid
// coupling the database package to the watchlist package.
func normalizeQuery(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
