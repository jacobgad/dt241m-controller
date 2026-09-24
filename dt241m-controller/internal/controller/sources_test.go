package controller_test

import (
	"reflect"
	"testing"

	"github.com/jacobgad/dt241m-controller/internal/controller"
	"github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/testutil"
)

const (
	txCamera  = "aa:aa:aa:aa:aa:01"
	txLectern = "aa:aa:aa:aa:aa:02"
	txSpare   = "aa:aa:aa:aa:aa:03"
)

func txNamed(mac, ip, name string, channel int, net *testutil.Network) *testutil.Device {
	return net.Place(ip, testutil.NewDevice(testutil.DeviceOptions{
		MAC: mac, Fixture: "tx-info-initial-channel-3",
		Overrides: map[string]any{"dev_name": name}, Reported: intp(channel),
	}))
}

func matrix(t *testing.T) (*harness, *testutil.Device, *testutil.Device) {
	t.Helper()
	h := newHarness(t, harnessOptions{})
	txNamed(txCamera, "192.168.1.2", "Stage Camera", 1, h.net)
	lectern := txNamed(txLectern, "192.168.1.3", "Lectern PC", 2, h.net)
	receiver := rx(testutil.RxFixtureMAC, rxIP, h.net)
	h.discover(t)
	return h, receiver, lectern
}

func sourceConfig(h *harness) map[string]any {
	return h.mqtt.DiscoveryConfigs()["homeassistant/select/"+rxID+"/source/config"]
}

func options(cfg map[string]any) []string {
	raw := cfg["options"].([]any)
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = v.(string)
	}
	return out
}

func TestReceiverSourceSelectListsTransmittersByName(t *testing.T) {
	h, _, _ := matrix(t)
	cfg := sourceConfig(h)
	if cfg["unique_id"] != rxID+"_source" || cfg["command_topic"] != "dt241m/device/fc19286cd6d8/source/set" || cfg["state_topic"] != "dt241m/device/fc19286cd6d8/source/state" || cfg["optimistic"] != false {
		t.Fatalf("cfg %v", cfg)
	}
	if got := options(cfg); !reflect.DeepEqual(got, []string{"Lectern PC", "Stage Camera"}) {
		t.Fatalf("options %v", got)
	}
	for _, opt := range options(cfg) {
		if opt == "none" {
			t.Fatal("none must not be selectable")
		}
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState) != "Lectern PC" {
		t.Fatalf("receiver on channel 2 should show Lectern PC, got %q", h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState))
	}
}

func TestSelectingASourceTunesTheReceiver(t *testing.T) {
	h, receiver, _ := matrix(t)
	outcome, err := h.ctrl.RequestSourceChange(h.ctx, testutil.RxFixtureMAC, "Stage Camera")
	if err != nil || outcome.Status != controller.StatusMatched || outcome.Requested != 1 {
		t.Fatalf("outcome %+v err %v", outcome, err)
	}
	if receiver.ReportedChannel() != 1 {
		t.Fatal("receiver not tuned")
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState) != "Stage Camera" || h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState) != "1" {
		t.Fatal("source and channel state must agree")
	}
	if len(h.net.WritesTo(rxIP)) != 1 || h.net.WritesTo(rxIP)[0].ChannelParam() != 1 {
		t.Fatal("expected exactly one set_channel_id with channel 1")
	}
}

func TestSourceCommandOverMQTT(t *testing.T) {
	h, receiver, _ := matrix(t)
	h.mqtt.Deliver(mqtt.ForDevice(testutil.RxFixtureMAC).SourceSet, "Stage Camera")
	eventually(t, func() bool { return receiver.ReportedChannel() == 1 }, "receiver to tune")
	eventually(t, func() bool {
		return h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState) == "Stage Camera"
	}, "source state")
}

func TestUnusedChannelShowsNoneButNoneIsRejectedAsCommand(t *testing.T) {
	h, receiver, _ := matrix(t)
	receiver.SetReported(9)
	h.ctrl.PollKnownDevices(h.ctx)
	if h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState) != "none" {
		t.Fatalf("expected none, got %q", h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState))
	}
	if _, err := h.ctrl.RequestSourceChange(h.ctx, testutil.RxFixtureMAC, "none"); err == nil {
		t.Fatal("none must not be accepted as a source")
	}
	if len(h.net.AllWrites()) != 0 {
		t.Fatal("no write expected")
	}
}

func TestSourceOptionsFollowTransmitterRenames(t *testing.T) {
	h, _, _ := matrix(t)
	h.ctrl.Rename(h.ctx, txLectern, "Presentation Laptop")
	if got := options(sourceConfig(h)); !reflect.DeepEqual(got, []string{"Presentation Laptop", "Stage Camera"}) {
		t.Fatalf("options %v", got)
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState) != "Presentation Laptop" {
		t.Fatal("state label not refreshed after rename")
	}
	if _, err := h.ctrl.RequestSourceChange(h.ctx, testutil.RxFixtureMAC, "Lectern PC"); err == nil {
		t.Fatal("old label must no longer resolve")
	}
}

func TestSourceOptionsFollowNewTransmitters(t *testing.T) {
	h, _, _ := matrix(t)
	txNamed(txSpare, "192.168.1.4", "Spare Player", 5, h.net)
	h.discover(t)
	if got := options(sourceConfig(h)); !reflect.DeepEqual(got, []string{"Lectern PC", "Spare Player", "Stage Camera"}) {
		t.Fatalf("options %v", got)
	}
}

func TestDuplicateTransmitterNamesAreDisambiguated(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	txNamed(txCamera, "192.168.1.2", "Camera", 1, h.net)
	txNamed(txLectern, "192.168.1.3", "Camera", 2, h.net)
	receiver := rx(testutil.RxFixtureMAC, rxIP, h.net)
	h.discover(t)
	if got := options(sourceConfig(h)); !reflect.DeepEqual(got, []string{"Camera (ch 1)", "Camera (ch 2)"}) {
		t.Fatalf("options %v", got)
	}
	if _, err := h.ctrl.RequestSourceChange(h.ctx, testutil.RxFixtureMAC, "Camera (ch 1)"); err != nil {
		t.Fatal(err)
	}
	if receiver.ReportedChannel() != 1 {
		t.Fatal("not tuned")
	}
}

func TestTransmitterChannelWriteAndSourceRefresh(t *testing.T) {
	h, _, lectern := matrix(t)
	outcome, err := h.ctrl.RequestChannelChange(h.ctx, txLectern, 7)
	if err != nil || outcome.Status != controller.StatusMatched {
		t.Fatalf("outcome %+v err %v", outcome, err)
	}
	if lectern.ReportedChannel() != 7 || h.mqtt.LastPayload(mqtt.ForDevice(txLectern).ChannelState) != "7" {
		t.Fatal("transmitter not updated")
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState) != "none" {
		t.Fatalf("receiver still on channel 2 should now show none, got %q", h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState))
	}
	if h.logs.Contains("transmitter_channel_collision") {
		t.Fatal("no collision expected")
	}
}

func TestTransmitterCollisionIsWarnedButAllowed(t *testing.T) {
	h, _, lectern := matrix(t)
	outcome, err := h.ctrl.RequestChannelChange(h.ctx, txLectern, 1)
	if err != nil || outcome.Status != controller.StatusMatched || lectern.ReportedChannel() != 1 {
		t.Fatalf("outcome %+v err %v", outcome, err)
	}
	if !h.logs.Contains("transmitter_channel_collision") {
		t.Fatal("collision should be logged")
	}
	if got := options(sourceConfig(h)); !reflect.DeepEqual(got, []string{"Lectern PC", "Stage Camera"}) {
		t.Fatalf("options %v", got)
	}
}

func TestTransmitterWriteOverMQTT(t *testing.T) {
	h, _, lectern := matrix(t)
	h.mqtt.Deliver(mqtt.ForDevice(txLectern).ChannelSet, "4")
	eventually(t, func() bool { return lectern.ReportedChannel() == 4 }, "transmitter write")
	eventually(t, func() bool { return h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState) == "none" }, "receiver source refresh")
}

func TestOfflineTransmitterStaysSelectable(t *testing.T) {
	h, _, _ := matrix(t)
	h.net.Remove("192.168.1.3")
	h.ctrl.PollKnownDevices(h.ctx)
	if got := options(sourceConfig(h)); !reflect.DeepEqual(got, []string{"Lectern PC", "Stage Camera"}) {
		t.Fatalf("options %v", got)
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).SourceState) != "Lectern PC" {
		t.Fatal("receiver still tuned to the offline transmitter's channel")
	}
}
