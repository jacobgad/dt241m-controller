package registry_test

import (
	"encoding/json"
	"reflect"
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

func txInfo(t *testing.T, mac, name string, channel int) *dt241m.DeviceInfo {
	return info(t, "tx-info-initial-channel-3", map[string]any{"lan_mac_addr": mac, "dev_name": name, "channel_id": channel})
}

func TestSourcesTable(t *testing.T) {
	t.Parallel()
	r := registry.New()
	r.RecordObservation("10.0.0.1", txInfo(t, "aa:aa:aa:aa:aa:01", "Stage Camera", 1), time.Now())
	r.RecordObservation("10.0.0.2", txInfo(t, "aa:aa:aa:aa:aa:02", "Lectern PC", 2), time.Now())
	r.RecordObservation("10.0.0.3", txInfo(t, "aa:aa:aa:aa:aa:03", "Lectern PC", 3), time.Now())
	r.RecordObservation("10.0.0.9", info(t, "rx-info-initial-channel-2", nil), time.Now())
	r.RecordObservation("10.0.0.8", info(t, "rx-info-initial-channel-2", map[string]any{"lan_mac_addr": "bb:bb:bb:bb:bb:01", "product_name": nil, "model": nil}), time.Now())

	table := r.Sources()
	if got := table.Options(); !reflect.DeepEqual(got, []string{"Lectern PC (ch 2)", "Lectern PC (ch 3)", "Stage Camera"}) {
		t.Fatalf("options %v", got)
	}
	if s, ok := table.ByLabel("Lectern PC (ch 3)"); !ok || s.MAC != "aa:aa:aa:aa:aa:03" || s.Channel != 3 {
		t.Fatalf("lookup %+v %v", s, ok)
	}
	if _, ok := table.ByLabel("Lectern PC"); ok {
		t.Fatal("ambiguous bare label must not resolve")
	}
	two, seven := 2, 7
	if label := table.ForChannel(&two); label != "Lectern PC (ch 2)" {
		t.Fatalf("channel 2 -> %q", label)
	}
	if label := table.ForChannel(&seven); label != registry.SourceNone {
		t.Fatalf("unused channel -> %q", label)
	}
	if label := table.ForChannel(nil); label != registry.SourceNone {
		t.Fatal("nil channel must be none")
	}
	if len(table.Collisions()) != 0 {
		t.Fatal("no collisions expected")
	}

	name := "Stage Camera"
	r.SetName("aa:aa:aa:aa:aa:02", &name)
	renamed := r.Sources()
	if renamed.Equal(table) {
		t.Fatal("rename must change the table")
	}
	if got := renamed.Options(); !reflect.DeepEqual(got, []string{"Lectern PC", "Stage Camera (ch 1)", "Stage Camera (ch 2)"}) {
		t.Fatalf("options after rename %v", got)
	}

	r.RecordObservation("10.0.0.1", txInfo(t, "aa:aa:aa:aa:aa:01", "Stage Camera", 2), time.Now())
	collided := r.Sources()
	if label := collided.ForChannel(&two); label == registry.SourceNone {
		t.Fatalf("collision should still resolve to a label, got %q", label)
	}
	if macs := collided.Collisions()[2]; len(macs) != 2 {
		t.Fatalf("collisions %v", collided.Collisions())
	}
	if !collided.Equal(r.Sources()) {
		t.Fatal("identical tables must be equal")
	}
}
