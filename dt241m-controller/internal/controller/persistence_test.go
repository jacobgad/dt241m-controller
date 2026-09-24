package controller_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/store"
	"github.com/jacobgad/dt241m-controller/internal/testutil"
)

func openStore(t *testing.T, name string) *store.SQLite {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func openStoreAt(t *testing.T, path string) *store.SQLite {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSQLiteStoreRoundTrip(t *testing.T) {
	s := openStore(t, "a.sqlite")
	if rows, _ := s.LoadAll(); len(rows) != 0 {
		t.Fatal("expected empty store")
	}
	h := newHarness(t, harnessOptions{store: s})
	rx(testutil.RxFixtureMAC, rxIP, h.net)
	h.discover(t)
	rows, err := s.LoadAll()
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v err %v", rows, err)
	}
	r := rows[0]
	if r.MAC != testutil.RxFixtureMAC || r.ID != rxID || r.Role != "receiver" || r.IP != rxIP || r.Name != nil ||
		*r.ReportedName != "ER02_286CD6D8" || *r.ProductName != "ProAVRx ER01" || *r.Firmware != "1.13471.133" || *r.Channel != 2 || r.Online {
		t.Fatalf("record %+v", r)
	}
	if r.LastSeenAt == nil || !r.LastSeenAt.Equal(r.FirstSeenAt) {
		t.Fatal("timestamps wrong")
	}
}

func TestReopeningDatabaseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.sqlite")
	first := openStoreAt(t, path)
	first.Close()
	second := openStoreAt(t, path)
	defer second.Close()
	if rows, err := second.LoadAll(); err != nil || len(rows) != 0 {
		t.Fatalf("rows %v err %v", rows, err)
	}
}

func TestOfflineAdapterSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.sqlite")
	net := testutil.NewNetwork()
	rx(testutil.RxFixtureMAC, rxIP, net)

	first := openStoreAt(t, path)
	before := newHarness(t, harnessOptions{store: first, net: net})
	if err := before.ctrl.Start(before.ctx); err != nil {
		t.Fatal(err)
	}
	before.ctrl.Stop(before.ctx)
	first.Close()

	net.Remove(rxIP)
	second := openStoreAt(t, path)
	defer second.Close()
	after := newHarness(t, harnessOptions{store: second, net: net})
	if err := after.ctrl.Start(after.ctx); err != nil {
		t.Fatal(err)
	}
	defer after.ctrl.Stop(after.ctx)

	a, ok := after.ctrl.Registry.Get(testutil.RxFixtureMAC)
	if !ok || a.Online || a.IP != rxIP || *a.Channel != 2 {
		t.Fatalf("adapter %+v ok=%v", a, ok)
	}
	configs := after.mqtt.DiscoveryConfigs()
	if _, ok := configs["homeassistant/number/"+rxID+"/channel/config"]; !ok {
		t.Fatal("discovery not republished")
	}
	if after.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).Availability) != "offline" ||
		after.mqtt.LastPayload(mqtt.ControllerKnownState) != "1" || after.mqtt.LastPayload(mqtt.ControllerOnlineState) != "0" {
		t.Fatal("offline adapter not published correctly")
	}
	if rows, _ := second.LoadAll(); len(rows) != 1 {
		t.Fatal("adapter was deleted")
	}
	if len(net.AllWrites()) != 0 {
		t.Fatal("restart wrote to hardware")
	}
}

func TestRestartProbesLastKnownIPBeforeFullScan(t *testing.T) {
	s := openStore(t, "probe.sqlite")
	net := testutil.NewNetwork()
	rx(testutil.RxFixtureMAC, rxIP, net)
	before := newHarness(t, harnessOptions{store: s, net: net})
	_ = before.ctrl.Start(before.ctx)
	before.ctrl.Stop(before.ctx)

	net.ResetRequests()
	after := newHarness(t, harnessOptions{store: s, net: net})
	_ = after.ctrl.Start(after.ctx)
	defer after.ctrl.Stop(after.ctx)

	if reqs := net.Requests(); len(reqs) == 0 || reqs[0].IP != rxIP {
		t.Fatal("last-known IP was not probed first")
	}
	a, _ := after.ctrl.Registry.Get(testutil.RxFixtureMAC)
	availability := after.mqtt.PayloadsOn(mqtt.ForDevice(testutil.RxFixtureMAC).Availability)
	if !a.Online || availability[0] != "offline" || availability[len(availability)-1] != "online" {
		t.Fatalf("availability %v online=%v", availability, a.Online)
	}
}

func TestRestartDoesNotReapplyRoutes(t *testing.T) {
	s := openStore(t, "routes.sqlite")
	net := testutil.NewNetwork()
	d := rx(testutil.RxFixtureMAC, rxIP, net)
	before := newHarness(t, harnessOptions{store: s, net: net})
	_ = before.ctrl.Start(before.ctx)
	if _, err := before.ctrl.RequestChannelChange(before.ctx, testutil.RxFixtureMAC, 7); err != nil {
		t.Fatal(err)
	}
	before.ctrl.Stop(before.ctx)

	d.SetReported(1)
	after := newHarness(t, harnessOptions{store: s, net: net})
	_ = after.ctrl.Start(after.ctx)
	defer after.ctrl.Stop(after.ctx)
	if d.ReportedChannel() != 1 || len(net.WritesTo(rxIP)) != 1 || after.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState) != "1" {
		t.Fatal("route was reapplied")
	}
}

func TestNameSurvivesRestart(t *testing.T) {
	s := openStore(t, "name.sqlite")
	net := testutil.NewNetwork()
	rx(testutil.RxFixtureMAC, rxIP, net)
	before := newHarness(t, harnessOptions{store: s, net: net})
	_ = before.ctrl.Start(before.ctx)
	before.ctrl.Rename(before.ctx, testutil.RxFixtureMAC, "Main Projector")
	before.ctrl.Stop(before.ctx)

	after := newHarness(t, harnessOptions{store: s, net: net})
	_ = after.ctrl.Start(after.ctx)
	defer after.ctrl.Stop(after.ctx)
	a, _ := after.ctrl.Registry.Get(testutil.RxFixtureMAC)
	if a.Name == nil || *a.Name != "Main Projector" || *a.ReportedName != "ER02_286CD6D8" {
		t.Fatalf("adapter %+v", a)
	}
	if after.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).NameState) != "Main Projector" {
		t.Fatal("name state wrong")
	}
	cfg := after.mqtt.DiscoveryConfigs()["homeassistant/number/"+rxID+"/channel/config"]
	if cfg["device"].(map[string]any)["name"] != "Main Projector" {
		t.Fatal("device name not applied")
	}
}

func TestDHCPMovePersistsOneRecordWithSameNameAndIdentity(t *testing.T) {
	s := openStore(t, "dhcp.sqlite")
	net := testutil.NewNetwork()
	rx(testutil.RxFixtureMAC, "192.168.1.2", net)
	h := newHarness(t, harnessOptions{store: s, net: net})
	_ = h.ctrl.Start(h.ctx)
	defer h.ctrl.Stop(h.ctx)
	h.ctrl.Rename(h.ctx, testutil.RxFixtureMAC, "Main Projector")
	before := h.mqtt.DiscoveryConfigs()

	net.Move("192.168.1.2", "192.168.1.8")
	h.ctrl.RunDiscovery(h.ctx, "dhcp")

	rows, _ := s.LoadAll()
	if len(rows) != 1 || rows[0].IP != "192.168.1.8" || *rows[0].Name != "Main Projector" {
		t.Fatalf("rows %+v", rows)
	}
	after := h.mqtt.DiscoveryConfigs()
	if len(after) != len(before) {
		t.Fatal("discovery topics changed")
	}
	count := 0
	for topic, cfg := range after {
		if !strings.Contains(topic, rxID) {
			continue
		}
		count++
		device := cfg["device"].(map[string]any)
		if device["identifiers"].([]any)[0] != "dt241m:fc19286cd6d8" || device["name"] != "Main Projector" {
			t.Fatalf("device %v", device)
		}
	}
	if count != 4 || after["homeassistant/number/"+rxID+"/channel/config"]["unique_id"] != rxID+"_channel" {
		t.Fatal("identity changed")
	}
}

func TestDiscoveryPreservesName(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	d := rx(testutil.RxFixtureMAC, rxIP, h.net)
	h.discover(t)
	h.ctrl.Rename(h.ctx, testutil.RxFixtureMAC, "Main Projector")
	d.SetTemplate("dev_name", "ER02_RENAMED_ON_DEVICE")
	d.SetTemplate("version", "1.13471.999")
	h.discover(t)
	a, _ := h.ctrl.Registry.Get(testutil.RxFixtureMAC)
	if *a.Name != "Main Projector" || *a.ReportedName != "ER02_RENAMED_ON_DEVICE" || *a.Firmware != "1.13471.999" {
		t.Fatalf("adapter %+v", a)
	}
	rows, _ := h.store.LoadAll()
	if *rows[0].Name != "Main Projector" || *rows[0].ReportedName != "ER02_RENAMED_ON_DEVICE" {
		t.Fatal("store wrong")
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).NameState) != "Main Projector" {
		t.Fatal("name state wrong")
	}
}

func TestRenameKeepsIdentity(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(testutil.RxFixtureMAC, rxIP, h.net)
	h.discover(t)
	h.ctrl.Rename(h.ctx, testutil.RxFixtureMAC, "Main Projector")
	before := h.mqtt.DiscoveryConfigs()
	h.mqtt.Clear()
	h.net.ResetRequests()
	h.ctrl.Rename(h.ctx, testutil.RxFixtureMAC, "Auditorium Projector")
	after := h.mqtt.DiscoveryConfigs()
	if len(after) != 4 {
		t.Fatalf("expected 4 republished configs, got %d", len(after))
	}
	for topic, cfg := range after {
		prev := before[topic]
		if cfg["unique_id"] != prev["unique_id"] || cfg["state_topic"] != prev["state_topic"] || cfg["command_topic"] != prev["command_topic"] {
			t.Fatalf("identity changed on %s", topic)
		}
		if cfg["device"].(map[string]any)["name"] != "Auditorium Projector" {
			t.Fatal("device name not updated")
		}
	}
	for _, p := range h.mqtt.Published() {
		if strings.Contains(p.Topic, "Projector") {
			t.Fatal("name leaked into topic")
		}
	}
	if len(h.net.Requests()) != 0 {
		t.Fatal("rename touched hardware")
	}
	rows, _ := h.store.LoadAll()
	if rows[0].MAC != testutil.RxFixtureMAC || *rows[0].Name != "Auditorium Projector" {
		t.Fatal("store wrong")
	}
}

func TestNameValidationAndClearing(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(testutil.RxFixtureMAC, rxIP, h.net)
	h.discover(t)
	nameTopic := mqtt.ForDevice(testutil.RxFixtureMAC).NameState

	h.ctrl.Rename(h.ctx, testutil.RxFixtureMAC, "  Lobby TV  ")
	a, _ := h.ctrl.Registry.Get(testutil.RxFixtureMAC)
	if *a.Name != "Lobby TV" {
		t.Fatal("not trimmed")
	}
	if o := h.ctrl.Rename(h.ctx, testutil.RxFixtureMAC, strings.Repeat("x", 65)); o.Renamed {
		t.Fatal("accepted too long")
	}
	if o := h.ctrl.Rename(h.ctx, testutil.RxFixtureMAC, "bad\x07name"); o.Renamed {
		t.Fatal("accepted control chars")
	}
	if o := h.ctrl.Rename(h.ctx, "00:11:22:33:44:55", "Ghost"); o.Renamed {
		t.Fatal("renamed unknown device")
	}
	if h.mqtt.LastPayload(nameTopic) != "Lobby TV" {
		t.Fatal("rejected rename changed state")
	}

	if o := h.ctrl.Rename(h.ctx, testutil.RxFixtureMAC, "   "); !o.Renamed || o.Name != nil {
		t.Fatalf("clear outcome %+v", o)
	}
	a, _ = h.ctrl.Registry.Get(testutil.RxFixtureMAC)
	if a.Name != nil || h.mqtt.LastPayload(nameTopic) != "ER02_286CD6D8" {
		t.Fatal("name not cleared")
	}
	rows, _ := h.store.LoadAll()
	if rows[0].Name != nil {
		t.Fatal("store not cleared")
	}

	h.mqtt.Deliver(mqtt.ForDevice(testutil.RxFixtureMAC).NameSet, "Stage Monitor")
	eventually(t, func() bool { return h.mqtt.LastPayload(nameTopic) == "Stage Monitor" }, "mqtt rename")

	cfg := h.mqtt.DiscoveryConfigs()["homeassistant/text/"+rxID+"/name/config"]
	avail := cfg["availability"].([]any)
	if len(avail) != 1 || avail[0].(map[string]any)["topic"] != mqtt.ControllerAvailability || cfg["max"] != 64.0 {
		t.Fatalf("name entity config %v", cfg)
	}
}
