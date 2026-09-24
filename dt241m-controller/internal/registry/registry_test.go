package registry_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mac"
	"github.com/jacobgad/dt241m-controller/internal/registry"
	"github.com/jacobgad/dt241m-controller/internal/testutil"
)

func info(t *testing.T, fixture string, overrides map[string]any) *dt241m.DeviceInfo {
	t.Helper()
	result := testutil.FixtureResult(fixture)
	for k, v := range overrides {
		result[k] = v
	}
	data, _ := json.Marshal(result)
	var d dt241m.DeviceInfo
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

func TestMACNormalization(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"FC:19:28:6C:D6:D8", "fc-19-28-6c-d6-d8", "FC19286CD6D8"} {
		if mac.Normalize(raw) != "fc:19:28:6c:d6:d8" {
			t.Fatalf("normalize %q", raw)
		}
	}
	for _, bad := range []string{"", "not-a-mac", "fc:19:28:6c:d6"} {
		if mac.Normalize(bad) != "" {
			t.Fatalf("accepted %q", bad)
		}
	}
	if mac.Compact("fc:19:28:6c:d6:d8") != "fc19286cd6d8" || mac.AdapterID("fc:19:28:6c:d6:d8") != "dt241m_fc19286cd6d8" {
		t.Fatal("derived ids wrong")
	}
}

func TestRegistryCreatesAndUpdatesByMAC(t *testing.T) {
	t.Parallel()
	r := registry.New()
	now := time.UnixMilli(1000)
	obs, ok := r.RecordObservation("192.168.1.20", info(t, "rx-info-initial-channel-2", nil), now)
	if !ok || !obs.Created || obs.Adapter.MAC != testutil.RxFixtureMAC || obs.Adapter.ID != "dt241m_fc19286cd6d8" || obs.Adapter.Role != dt241m.RoleReceiver || *obs.Adapter.Channel != 2 || !obs.Adapter.FirstSeenAt.Equal(now) {
		t.Fatalf("obs %+v", obs)
	}
	moved, _ := r.RecordObservation("192.168.1.50", info(t, "rx-info-initial-channel-2", nil), now.Add(time.Second))
	if moved.Created || !moved.StateChanged || moved.PreviousIP != "192.168.1.20" || len(r.All()) != 1 {
		t.Fatalf("moved %+v", moved)
	}
}

func TestRegistryDisplacesButDoesNotMutateOldIdentity(t *testing.T) {
	t.Parallel()
	r := registry.New()
	r.RecordObservation("192.168.1.20", info(t, "rx-info-initial-channel-2", nil), time.Now())
	obs, _ := r.RecordObservation("192.168.1.20", info(t, "tx-info-initial-channel-3", nil), time.Now())
	if !obs.Created || obs.Adapter.MAC != testutil.TxFixtureMAC || obs.Displaced == nil || obs.Displaced.MAC != testutil.RxFixtureMAC {
		t.Fatalf("obs %+v", obs)
	}
	old, _ := r.Lookup(testutil.RxFixtureMAC)
	if old.ID != "dt241m_fc19286cd6d8" || old.Role != dt241m.RoleReceiver || old.Online || len(r.All()) != 2 {
		t.Fatalf("old %+v", old)
	}
}

func TestRegistryTransitionsAndCounts(t *testing.T) {
	t.Parallel()
	r := registry.New()
	r.RecordObservation("192.168.1.20", info(t, "rx-info-initial-channel-2", nil), time.Now())
	if _, changed := r.MarkOffline(testutil.RxFixtureMAC); !changed {
		t.Fatal("first markOffline should change")
	}
	if _, changed := r.MarkOffline(testutil.RxFixtureMAC); changed {
		t.Fatal("second markOffline should be a no-op")
	}
	if c := r.Counts(); c.Known != 1 || c.Online != 0 {
		t.Fatalf("counts %+v", c)
	}
	obs, _ := r.RecordObservation("192.168.1.20", info(t, "rx-info-initial-channel-2", map[string]any{"channel_id": 7}), time.Now())
	if !obs.CameOnline || !obs.StateChanged || *obs.Adapter.Channel != 7 {
		t.Fatalf("obs %+v", obs)
	}
	if _, ok := r.RecordObservation("192.168.1.20", info(t, "rx-info-initial-channel-2", map[string]any{"lan_mac_addr": "garbage"}), time.Now()); ok {
		t.Fatal("invalid MAC accepted")
	}
}

func TestRegistryRoleChangeAndNames(t *testing.T) {
	t.Parallel()
	r := registry.New()
	r.RecordObservation("192.168.1.20", info(t, "rx-info-initial-channel-2", map[string]any{"product_name": nil, "model": nil}), time.Now())
	a, _ := r.Lookup(testutil.RxFixtureMAC)
	if a.Role != dt241m.RoleUnknown {
		t.Fatal("expected unknown")
	}
	obs, _ := r.RecordObservation("192.168.1.20", info(t, "rx-info-initial-channel-2", nil), time.Now())
	if !obs.RoleChanged || obs.Adapter.Role != dt241m.RoleReceiver {
		t.Fatal("role change not flagged")
	}
	name := "Main Projector"
	updated, _ := r.SetName(testutil.RxFixtureMAC, &name)
	if updated.DisplayName() != "Main Projector" {
		t.Fatal("display name")
	}
	r.RecordObservation("192.168.1.20", info(t, "rx-info-initial-channel-2", map[string]any{"dev_name": "CHANGED"}), time.Now())
	a, _ = r.Lookup(testutil.RxFixtureMAC)
	if *a.Name != "Main Projector" || a.ReportedName != "CHANGED" {
		t.Fatal("observation overwrote name")
	}
	if _, err := registry.ValidateName("bad\x01"); err == nil {
		t.Fatal("control chars accepted")
	}
	if n, err := registry.ValidateName("   "); err != nil || n != nil {
		t.Fatal("blank should clear")
	}
}
