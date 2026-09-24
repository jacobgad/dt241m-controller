package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mac"
	"github.com/jacobgad/dt241m-controller/internal/registry"
	_ "modernc.org/sqlite"
)

type Store interface {
	LoadAll() ([]registry.Adapter, error)
	Save(a registry.Adapter) error
	SetName(macAddr string, name *string) error
}

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

type SQLite struct {
	db *sql.DB
}

func Open(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// why: modernc's driver is not safe for concurrent writers on one connection; a single pooled connection serialises us.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA journal_mode = WAL", "PRAGMA synchronous = NORMAL", "PRAGMA busy_timeout = 5000"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLite{db: db}, nil
}

func migrate(db *sql.DB) error {
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	if current > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than this build supports (%d)", current, schemaVersion)
	}
	for v := current; v < schemaVersion; v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) LoadAll() ([]registry.Adapter, error) {
	rows, err := s.db.Query(`SELECT mac, name, reported_name, role, last_known_ip, last_known_channel,
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
		a.Name = nullStr(name)
		a.ReportedName = nullStr(reported)
		a.ProductName = nullStr(product)
		a.Model = nullStr(model)
		a.Firmware = nullStr(fw)
		if ip.Valid {
			a.IP = ip.String
		}
		if channel.Valid {
			c := int(channel.Int64)
			a.Channel = &c
		}
		a.FirstSeenAt = time.UnixMilli(firstSeen)
		if lastSeen.Valid {
			t := time.UnixMilli(lastSeen.Int64)
			a.LastSeenAt = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLite) Save(a registry.Adapter) error {
	var lastSeen any
	if a.LastSeenAt != nil {
		lastSeen = a.LastSeenAt.UnixMilli()
	}
	var ip any
	if a.IP != "" {
		ip = a.IP
	}
	_, err := s.db.Exec(`INSERT INTO adapters
		(mac, name, reported_name, role, last_known_ip, last_known_channel, product_name, model, firmware, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(mac) DO UPDATE SET
			name = excluded.name,
			reported_name = excluded.reported_name,
			role = excluded.role,
			last_known_ip = excluded.last_known_ip,
			last_known_channel = excluded.last_known_channel,
			product_name = excluded.product_name,
			model = excluded.model,
			firmware = excluded.firmware,
			first_seen_at = excluded.first_seen_at,
			last_seen_at = excluded.last_seen_at`,
		a.MAC, ptr(a.Name), ptr(a.ReportedName), string(a.Role), ip, ptrInt(a.Channel),
		ptr(a.ProductName), ptr(a.Model), ptr(a.Firmware), a.FirstSeenAt.UnixMilli(), lastSeen)
	return err
}

func (s *SQLite) SetName(macAddr string, name *string) error {
	res, err := s.db.Exec("UPDATE adapters SET name = ? WHERE mac = ?", ptr(name), macAddr)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("adapter not found")
	}
	return nil
}

func nullStr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func ptr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func ptrInt(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}
