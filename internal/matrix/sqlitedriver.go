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

func init() {
	sql.Register("sqlite3-fk-wal", &fkWALDriver{inner: &sqlite.Driver{}})
}
