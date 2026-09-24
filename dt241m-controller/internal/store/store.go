// Package store persists the adapter inventory in SQLite under /data.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mac"
	"github.com/jacobgad/dt241m-controller/internal/registry"
	_ "modernc.org/sqlite"
)

// Store is the persistence boundary used by the controller.
//
// Save owns every column except name; SetName owns name. Keeping the two
// disjoint is what lets a poll snapshot taken before a rename be persisted
// afterwards without clobbering the rename.
type Store interface {
	LoadAll(ctx context.Context) ([]registry.Adapter, error)
	Save(ctx context.Context, a registry.Adapter) error
	SetName(ctx context.Context, macAddr string, name *string) error
}

// ErrNotFound is returned when a MAC has no row.
var ErrNotFound = errors.New("store: adapter not found")

const schemaVersion = 1

var migrations = []string{
	`CREATE TABLE adapters (
		mac TEXT PRIMARY KEY NOT NULL,
		name TEXT,
		reported_name TEXT,
		role TEXT NOT NULL DEFAULT 'unknown',
		last_known_ip TEXT,
		last_known_channel INTEGER,
		product_name TEXT,
		model TEXT,
		firmware TEXT,
		first_seen_at INTEGER NOT NULL,
		last_seen_at INTEGER
	)`,
}

// SQLite is the production Store.
type SQLite struct {
	db *sql.DB
}

// Open opens or creates the database at path and brings its schema up to date.
func Open(ctx context.Context, path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// why: one pooled connection avoids SQLITE_BUSY between the poller and command goroutines.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA journal_mode = WAL", "PRAGMA synchronous = NORMAL", "PRAGMA busy_timeout = 5000"} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return nil, errors.Join(fmt.Errorf("%s: %w", pragma, err), db.Close())
		}
	}
	if err := migrate(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &SQLite{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	if current == 0 {
		adopted, err := adoptLegacySchema(ctx, db)
		if err != nil {
			return err
		}
		if adopted {
			current = 1
		}
	}
	if current > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than this build supports (%d)", current, schemaVersion)
	}
	for v := current; v < schemaVersion; v++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			return errors.Join(fmt.Errorf("migration %d: %w", v+1, err), tx.Rollback())
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			return errors.Join(err, tx.Rollback())
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// adoptLegacySchema recognises a database created by the 1.x (Drizzle) release, whose adapters
// table is identical to schema version 1 but which never set user_version.
func adoptLegacySchema(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'adapters'").Scan(&count)
	if err != nil || count == 0 {
		return false, err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		return false, err
	}
	return true, nil
}

// Close releases the database handle.
func (s *SQLite) Close() error { return s.db.Close() }

// LoadAll returns every persisted adapter, marked offline, ordered by MAC.
func (s *SQLite) LoadAll(ctx context.Context) ([]registry.Adapter, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mac, name, reported_name, role, last_known_ip, last_known_channel,
		product_name, model, firmware, first_seen_at, last_seen_at FROM adapters ORDER BY mac`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []registry.Adapter
	for rows.Next() {
		var (
			a                                      registry.Adapter
			name, reported, ip, product, model, fw sql.NullString
			role                                   string
			channel, lastSeen                      sql.NullInt64
			firstSeen                              int64
		)
		if err := rows.Scan(&a.MAC, &name, &reported, &role, &ip, &channel, &product, &model, &fw, &firstSeen, &lastSeen); err != nil {
			return nil, err
		}
		a.ID = mac.AdapterID(a.MAC)
		a.Role = dt241m.Role(role)
		if name.Valid {
			a.Name = &name.String
		}
		a.ReportedName = reported.String
		a.ProductName = product.String
		a.Model = model.String
		a.Firmware = fw.String
		a.IP = ip.String
		if channel.Valid {
			c := int(channel.Int64)
			a.Channel = &c
		}
		a.FirstSeenAt = time.UnixMilli(firstSeen)
		if lastSeen.Valid {
			a.LastSeenAt = time.UnixMilli(lastSeen.Int64)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Save upserts the hardware-observed columns of a; name is left untouched on conflict.
func (s *SQLite) Save(ctx context.Context, a registry.Adapter) error {
	var lastSeen any
	if !a.LastSeenAt.IsZero() {
		lastSeen = a.LastSeenAt.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO adapters
		(mac, name, reported_name, role, last_known_ip, last_known_channel, product_name, model, firmware, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(mac) DO UPDATE SET
			reported_name = excluded.reported_name,
			role = excluded.role,
			last_known_ip = excluded.last_known_ip,
			last_known_channel = excluded.last_known_channel,
			product_name = excluded.product_name,
			model = excluded.model,
			firmware = excluded.firmware,
			first_seen_at = excluded.first_seen_at,
			last_seen_at = excluded.last_seen_at`,
		a.MAC, nullable(a.Name), emptyNull(a.ReportedName), string(a.Role), emptyNull(a.IP), nullableInt(a.Channel),
		emptyNull(a.ProductName), emptyNull(a.Model), emptyNull(a.Firmware), a.FirstSeenAt.UnixMilli(), lastSeen)
	return err
}

// SetName stores the user-chosen name (nil clears it) for an existing adapter.
func (s *SQLite) SetName(ctx context.Context, macAddr string, name *string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE adapters SET name = ? WHERE mac = ?", nullable(name), macAddr)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func nullable(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func emptyNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}
