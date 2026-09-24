package controller_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/discovery"
	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/testutil"
)

const (
	rxA = "aa:aa:aa:aa:aa:aa"
	rxB = "bb:bb:bb:bb:bb:bb"
)

func TestProbeFindsDevicesAmongUnresponsiveAddresses(t *testing.T) {
	net := testutil.NewNetwork()
	rx(testutil.RxFixtureMAC, "192.168.1.5", net)
	tx(testutil.TxFixtureMAC, "192.168.1.9", net)
	client := dt241m.NewHTTPClient(dt241m.Options{Transport: net, Timeout: 100 * time.Millisecond})
	hits := discovery.Probe(context.Background(), client, ips14(), discovery.Options{Concurrency: 4, Timeout: 100 * time.Millisecond})
	if len(hits) != 2 {
		t.Fatalf("hits %d", len(hits))
	}
	if len(net.Requests()) != 14 {
		t.Fatalf("expected every address probed, got %d", len(net.Requests()))
	}
}

func ips14() []string {
	out := make([]string, 0, 14)
	for i := 1; i <= 14; i++ {
		out = append(out, fmt.Sprintf("192.168.1.%d", i))
	}
	return out
}

func TestProbeContinuesPastHangingAddresses(t *testing.T) {
	net := testutil.NewNetwork()
	net.Unreachable = testutil.Hang
	rx(testutil.RxFixtureMAC, "192.168.1.14", net)
	client := dt241m.NewHTTPClient(dt241m.Options{Transport: net, Timeout: 30 * time.Millisecond})
	hits := discovery.Probe(context.Background(), client, ips14(), discovery.Options{Concurrency: 8, Timeout: 30 * time.Millisecond})
	if len(hits) != 1 || hits[0].IP != "192.168.1.14" {
		t.Fatalf("hits %+v", hits)
	}
}

func TestProbeRespectsConcurrencyLimit(t *testing.T) {
	net := testutil.NewNetwork()
	net.Unreachable = testutil.Hang
	client := dt241m.NewHTTPClient(dt241m.Options{Transport: net, Timeout: 20 * time.Millisecond})
	peak := 0
	discovery.Probe(context.Background(), client, ips14(), discovery.Options{
		Concurrency: 3, Timeout: 20 * time.Millisecond,
		OnProbeStart: func(_ string, active int) {
			if active > peak {
				peak = active
			}
		},
	})
	if peak != 3 {
		t.Fatalf("peak concurrency %d", peak)
	}
}

func TestDiscoveryRegistersClassifiesAndPublishes(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(testutil.RxFixtureMAC, "192.168.1.5", h.net)
	tx(testutil.TxFixtureMAC, "192.168.1.9", h.net)
	h.discover(t)

	r, _ := h.ctrl.Registry.Get(testutil.RxFixtureMAC)
	x, _ := h.ctrl.Registry.Get(testutil.TxFixtureMAC)
	if r.Role != dt241m.RoleReceiver || r.IP != "192.168.1.5" || x.Role != dt241m.RoleTransmitter || x.IP != "192.168.1.9" {
		t.Fatalf("rx %+v tx %+v", r, x)
	}
	configs := h.mqtt.DiscoveryConfigs()
	if _, ok := configs["homeassistant/number/dt241m_fc19286cd6d8/channel/config"]; !ok {
		t.Fatal("receiver number config missing")
	}
	if _, ok := configs["homeassistant/sensor/dt241m_fc19286cd291/channel/config"]; !ok {
		t.Fatal("transmitter sensor config missing")
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState) != "2" || h.mqtt.LastPayload(mqtt.ForDevice(testutil.TxFixtureMAC).ChannelState) != "3" {
		t.Fatal("channel states wrong")
	}
	if h.mqtt.LastPayload(mqtt.ControllerKnownState) != "2" || h.mqtt.LastPayload(mqtt.ControllerOnlineState) != "2" {
		t.Fatal("counts wrong")
	}
}

func TestSameMACAtNewIPUpdatesExistingDevice(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(testutil.RxFixtureMAC, "192.168.1.5", h.net)
	h.discover(t)
	h.net.Move("192.168.1.5", "192.168.1.11")
	h.discover(t)
	if len(h.ctrl.Registry.All()) != 1 {
		t.Fatal("duplicate adapter")
	}
	a, _ := h.ctrl.Registry.Get(testutil.RxFixtureMAC)
	if a.IP != "192.168.1.11" || h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).IPState) != "192.168.1.11" {
		t.Fatalf("ip not updated: %+v", a)
	}
	if h.mqtt.LastPayload(mqtt.ControllerKnownState) != "1" {
		t.Fatal("known count wrong")
	}
}

func TestDifferentMACAtOldIPDoesNotMutateIdentity(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(rxA, "192.168.1.5", h.net)
	h.discover(t)
	h.net.Remove("192.168.1.5")
	rx(rxB, "192.168.1.5", h.net, func(o *testutil.DeviceOptions) { o.Overrides = map[string]any{"dev_name": "ER02_B"} })
	h.discover(t)

	a, _ := h.ctrl.Registry.Get(rxA)
	b, _ := h.ctrl.Registry.Get(rxB)
	if a.ID != "dt241m_aaaaaaaaaaaa" || *a.ReportedName != "ER02_286CD6D8" || a.Online {
		t.Fatalf("a mutated: %+v", a)
	}
	if b.ID != "dt241m_bbbbbbbbbbbb" || b.IP != "192.168.1.5" || !b.Online {
		t.Fatalf("b wrong: %+v", b)
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(rxA).Availability) != "offline" || h.mqtt.LastPayload(mqtt.ForDevice(rxB).Availability) != "online" {
		t.Fatal("availability wrong")
	}
}

func TestRepeatedDiscoveryDoesNotDuplicateHADevice(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(testutil.RxFixtureMAC, "192.168.1.5", h.net)
	for i := 0; i < 3; i++ {
		h.discover(t)
	}
	topics := map[string]bool{}
	uniqueIDs := map[string]bool{}
	for topic, cfg := range h.mqtt.DiscoveryConfigs() {
		if !strings.Contains(topic, "dt241m_fc19286cd6d8") {
			continue
		}
		topics[topic] = true
		uniqueIDs[cfg["unique_id"].(string)] = true
		ids := cfg["device"].(map[string]any)["identifiers"].([]any)
		if len(ids) != 1 || ids[0] != "dt241m:fc19286cd6d8" {
			t.Fatalf("identifiers %v", ids)
		}
	}
	expected := []string{
		"homeassistant/number/dt241m_fc19286cd6d8/channel/config",
		"homeassistant/sensor/dt241m_fc19286cd6d8/ip_address/config",
		"homeassistant/sensor/dt241m_fc19286cd6d8/role/config",
		"homeassistant/text/dt241m_fc19286cd6d8/name/config",
	}
	if len(topics) != len(expected) || len(uniqueIDs) != len(expected) {
		t.Fatalf("topics %v ids %v", topics, uniqueIDs)
	}
	for _, e := range expected {
		if !topics[e] {
			t.Fatalf("missing %s", e)
		}
	}
}

func TestNoOverlappingDiscovery(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.net.Unreachable = testutil.Hang
	done := make(chan struct{}, 2)
	go func() { h.ctrl.RunDiscovery(h.ctx, "a"); done <- struct{}{} }()
	eventually(t, h.ctrl.DiscoveryRunning, "discovery to start")
	go func() { h.ctrl.RunDiscovery(h.ctx, "b"); done <- struct{}{} }()
	<-done
	<-done
	if h.ctrl.DiscoveryRunCount() != 1 || h.ctrl.DiscoveryRunning() {
		t.Fatalf("runs %d running %v", h.ctrl.DiscoveryRunCount(), h.ctrl.DiscoveryRunning())
	}
}

func TestOnlyProbesConfiguredRange(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	h.discover(t)
	probed := map[string]bool{}
	for _, r := range h.net.Requests() {
		probed[r.IP] = true
	}
	if len(probed) != 14 || probed["192.168.1.0"] || probed["192.168.1.15"] {
		t.Fatalf("probed %v", probed)
	}
}

func TestPollingPublishesPhysicalChannelChangeWithoutWriting(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	d := rx(testutil.RxFixtureMAC, "192.168.1.5", h.net)
	h.discover(t)
	d.SetReported(6)
	d.FrontPanel = 6
	h.ctrl.PollKnownDevices(h.ctx)
	a, _ := h.ctrl.Registry.Get(testutil.RxFixtureMAC)
	if *a.Channel != 6 || h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState) != "6" {
		t.Fatal("channel not observed")
	}
	if len(h.net.WritesTo("192.168.1.5")) != 0 {
		t.Fatal("poll wrote to device")
	}
}

func TestPollingNeverReappliesChannels(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	d := rx(testutil.RxFixtureMAC, "192.168.1.5", h.net)
	h.discover(t)
	if _, err := h.ctrl.RequestChannelChange(h.ctx, testutil.RxFixtureMAC, 4); err != nil {
		t.Fatal(err)
	}
	d.SetReported(1)
	h.ctrl.PollKnownDevices(h.ctx)
	h.ctrl.PollKnownDevices(h.ctx)
	if len(h.net.WritesTo("192.168.1.5")) != 1 {
		t.Fatal("poll reapplied channel")
	}
	a, _ := h.ctrl.Registry.Get(testutil.RxFixtureMAC)
	if *a.Channel != 1 {
		t.Fatal("registry should track hardware")
	}
}

func TestVanishedDeviceIsRediscoveredAtNewIP(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(testutil.RxFixtureMAC, "192.168.1.5", h.net)
	h.discover(t)
	runs := h.ctrl.DiscoveryRunCount()
	h.net.Move("192.168.1.5", "192.168.1.12")
	h.mqtt.Clear()
	h.ctrl.PollKnownDevices(h.ctx)

	availability := h.mqtt.PayloadsOn(mqtt.ForDevice(testutil.RxFixtureMAC).Availability)
	if len(availability) != 2 || availability[0] != "offline" || availability[1] != "online" {
		t.Fatalf("availability %v", availability)
	}
	if h.ctrl.DiscoveryRunCount() != runs+1 {
		t.Fatal("rediscovery not triggered")
	}
	a, _ := h.ctrl.Registry.Get(testutil.RxFixtureMAC)
	if a.IP != "192.168.1.12" || len(h.ctrl.Registry.All()) != 1 {
		t.Fatalf("adapter %+v", a)
	}
}

func TestUnfoundDeviceStaysOffline(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(testutil.RxFixtureMAC, "192.168.1.5", h.net)
	h.discover(t)
	h.net.Remove("192.168.1.5")
	h.ctrl.PollKnownDevices(h.ctx)
	a, _ := h.ctrl.Registry.Get(testutil.RxFixtureMAC)
	if a.Online || h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).Availability) != "offline" {
		t.Fatal("should be offline")
	}
	if h.mqtt.LastPayload(mqtt.ControllerOnlineState) != "0" || h.mqtt.LastPayload(mqtt.ControllerKnownState) != "1" {
		t.Fatal("counts wrong")
	}
}
