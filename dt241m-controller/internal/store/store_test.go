package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/registry"
	"github.com/jacobgad/dt241m-controller/internal/store"
	_ "modernc.org/sqlite"
)

const drizzleSchema = `
CREATE TABLE IF NOT EXISTS "__drizzle_migrations" (id SERIAL PRIMARY KEY, hash text NOT NULL, created_at numeric);
INSERT INTO "__drizzle_migrations" (hash, created_at) VALUES ('abc', 1790217467168);
CREATE TABLE ` + "`adapters`" + ` (
	` + "`mac`" + ` text PRIMARY KEY NOT NULL,
	` + "`name`" + ` text,
	` + "`reported_name`" + ` text,
	` + "`role`" + ` text DEFAULT 'unknown' NOT NULL,
	` + "`last_known_ip`" + ` text,
	` + "`last_known_channel`" + ` integer,
	` + "`product_name`" + ` text,
	` + "`model`" + ` text,
	` + "`firmware`" + ` text,
	` + "`first_seen_at`" + ` integer NOT NULL,
	` + "`last_seen_at`" + ` integer
);
INSERT INTO adapters VALUES ('aa:aa:aa:aa:aa:aa', 'Main Projector', 'ER02_AAAA', 'receiver', '192.168.1.20', 2, 'ProAVRx ER01', 'am_8270_proavrx-eth_er01-pway-dt241', '1.13471.133', 1700000000000, 1700000060000);
`

func TestOpenAdoptsDatabaseCreatedByVersion1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.sqlite")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(drizzleSchema); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("opening a 1.x database must not fail: %v", err)
	}
	defer s.Close()
	rows, err := s.LoadAll(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v err %v", rows, err)
	}
	a := rows[0]
	if a.MAC != "aa:aa:aa:aa:aa:aa" || *a.Name != "Main Projector" || *a.ReportedName != "ER02_AAAA" || a.Role != "receiver" ||
		a.IP != "192.168.1.20" || *a.Channel != 2 || !a.FirstSeenAt.Equal(time.UnixMilli(1700000000000)) || !a.LastSeenAt.Equal(time.UnixMilli(1700000060000)) {
		t.Fatalf("adapter %+v", a)
	}

	s.Close()
	again, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	again.Close()
}

func TestSaveDoesNotOverwriteName(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "name.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	a := registry.Adapter{MAC: "aa:aa:aa:aa:aa:aa", ID: "dt241m_aaaaaaaaaaaa", Role: "receiver", IP: "10.0.0.1", FirstSeenAt: now, LastSeenAt: &now}
	if err := s.Save(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	name := "Main Projector"
	if err := s.SetName(context.Background(), a.MAC, &name); err != nil {
		t.Fatal(err)
	}
	stale := a
	stale.Name = nil
	stale.IP = "10.0.0.2"
	if err := s.Save(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.LoadAll(context.Background())
	if rows[0].Name == nil || *rows[0].Name != "Main Projector" || rows[0].IP != "10.0.0.2" {
		t.Fatalf("row %+v", rows[0])
	}
	if err := s.SetName(context.Background(), "00:00:00:00:00:00", &name); err == nil {
		t.Fatal("SetName on unknown adapter should fail")
	}
}
