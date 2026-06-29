package matrix

import (
	"database/sql"
	"database/sql/driver"

	sqlite "modernc.org/sqlite"
)

type fkWALDriver struct {
	inner *sqlite.Driver
}

func (d *fkWALDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	execer := conn.(driver.Execer)
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := execer.Exec(pragma, nil); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// cryptoDriverName is a Pastel-private driver name. mautrix's cryptohelper
// blank-imports go.mau.fi/util/dbutil/litestream, whose init() registers the
// cgo "sqlite3-fk-wal" driver — so we cannot reuse that name without a
// "sql: Register called twice for driver sqlite3-fk-wal" panic at startup.
// We register our cgo-free modernc driver under our own name and hand
// cryptohelper a *dbutil.Database built on it (see newCryptoDB in client.go),
// keeping the crypto store cgo-free. The name keeps the "sqlite" prefix so
// dbutil.ParseDialect resolves it to the SQLite dialect.
const cryptoDriverName = "sqlite3-fk-wal-pastel"

func init() {
	sql.Register(cryptoDriverName, &fkWALDriver{inner: &sqlite.Driver{}})
}
