package controller_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/jacobgad/dt241m-controller/internal/controller"
	"github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/testutil"
)

const (
	rxIP = "192.168.1.5"
	txIP = "192.168.1.9"
	rxID = "dt241m_fc19286cd6d8"
	txID = "dt241m_fc19286cd291"
)

func started(t *testing.T) (*harness, *testutil.Device, *testutil.Device) {
	t.Helper()
	h := newHarness(t, harnessOptions{})
	r := rx(testutil.RxFixtureMAC, rxIP, h.net)
	x := tx(testutil.TxFixtureMAC, txIP, h.net)
	if err := h.ctrl.Start(h.ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.ctrl.Stop(h.ctx) })
	return h, r, x
}

func TestReceiverChannelDiscoveryPayload(t *testing.T) {
	h, _, _ := started(t)
	cfg := h.mqtt.DiscoveryConfigs()["homeassistant/number/"+rxID+"/channel/config"]
	expect := map[string]any{
		"name": "Channel", "unique_id": rxID + "_channel",
		"state_topic": "dt241m/device/fc19286cd6d8/channel/state", "command_topic": "dt241m/device/fc19286cd6d8/channel/set",
		"min": 0.0, "max": 255.0, "step": 1.0, "mode": "box", "optimistic": false, "retain": false, "availability_mode": "all", "icon": "mdi:monitor",
	}
	for k, v := range expect {
		if cfg[k] != v {
			t.Fatalf("%s = %v, want %v", k, cfg[k], v)
		}
	}
	device := cfg["device"].(map[string]any)
	if device["name"] != "ER02_286CD6D8" || device["manufacturer"] != "PWAY" || device["model"] != "ProAVRx ER01" || device["sw_version"] != "1.13471.133" {
		t.Fatalf("device %v", device)
	}
	if conns := device["connections"].([]any)[0].([]any); conns[0] != "mac" || conns[1] != testutil.RxFixtureMAC {
		t.Fatalf("connections %v", conns)
	}
	avail := cfg["availability"].([]any)
	if len(avail) != 2 || avail[0].(map[string]any)["topic"] != mqtt.ControllerAvailability || avail[1].(map[string]any)["topic"] != mqtt.ForDevice(testutil.RxFixtureMAC).Availability {
		t.Fatalf("availability %v", avail)
	}
}

func TestTransmitterChannelIsAConfigNumber(t *testing.T) {
	h, _, _ := started(t)
	configs := h.mqtt.DiscoveryConfigs()
	cfg := configs["homeassistant/number/"+txID+"/channel/config"]
	if cfg["unique_id"] != txID+"_channel" || cfg["icon"] != "mdi:broadcast" || cfg["command_topic"] != "dt241m/device/fc19286cd291/channel/set" || cfg["entity_category"] != "config" || cfg["optimistic"] != false {
		t.Fatalf("cfg %v", cfg)
	}
	if _, exists := configs["homeassistant/sensor/"+txID+"/channel/config"]; exists {
		t.Fatal("legacy sensor config must be cleared")
	}
	if _, exists := configs["homeassistant/select/"+txID+"/source/config"]; exists {
		t.Fatal("transmitters must not have a Source select")
	}
}

func TestRoleSensorAndControllerEntities(t *testing.T) {
	h, _, _ := started(t)
	configs := h.mqtt.DiscoveryConfigs()
	role := configs["homeassistant/sensor/"+rxID+"/role/config"]
	if role["unique_id"] != rxID+"_role" || role["entity_category"] != "diagnostic" {
		t.Fatalf("role %v", role)
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).RoleState) != "receiver" || h.mqtt.LastPayload(mqtt.ForDevice(testutil.TxFixtureMAC).RoleState) != "transmitter" {
		t.Fatal("role states wrong")
	}
	button := configs["homeassistant/button/dt241m_controller/rescan/config"]
	if button["command_topic"] != mqtt.ControllerRescanPress || button["payload_press"] != "PRESS" || button["retain"] != false {
		t.Fatalf("button %v", button)
	}
	if button["device"].(map[string]any)["identifiers"].([]any)[0] != "dt241m:controller" {
		t.Fatal("controller identifier wrong")
	}
	if configs["homeassistant/sensor/dt241m_controller/known_devices/config"]["state_topic"] != mqtt.ControllerKnownState {
		t.Fatal("known devices sensor wrong")
	}
	if configs["homeassistant/sensor/dt241m_controller/online_devices/config"]["state_topic"] != mqtt.ControllerOnlineState {
		t.Fatal("online devices sensor wrong")
	}
}

func TestTopicsNeverContainIPsOrNamesAndUniqueIDsSurviveIPChange(t *testing.T) {
	h, _, _ := started(t)
	before := h.mqtt.DiscoveryConfigs()
	h.net.Move(rxIP, "192.168.1.12")
	h.ctrl.PollKnownDevices(h.ctx)
	after := h.mqtt.DiscoveryConfigs()
	ipPattern := regexp.MustCompile(`\d+\.\d+\.\d+\.\d+`)
	for topic, cfg := range after {
		if cfg["unique_id"] != before[topic]["unique_id"] {
			t.Fatalf("unique_id changed on %s", topic)
		}
	}
	for _, p := range h.mqtt.Published() {
		if ipPattern.MatchString(p.Topic) || strings.Contains(p.Topic, "ER02") || strings.Contains(p.Topic, "ET01") {
			t.Fatalf("topic leaks identity: %s", p.Topic)
		}
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).IPState) != "192.168.1.12" {
		t.Fatal("ip state not updated")
	}
}

func TestAvailabilityAndSubscriptions(t *testing.T) {
	h, _, _ := started(t)
	for _, topic := range []string{mqtt.ControllerAvailability, mqtt.ForDevice(testutil.RxFixtureMAC).Availability, mqtt.ForDevice(testutil.TxFixtureMAC).Availability} {
		if last, _ := h.mqtt.LastOn(topic); last.Payload != "online" || !last.Retain {
			t.Fatalf("%s: %+v", topic, last)
		}
	}
	subs := strings.Join(h.mqtt.Subscriptions(), " ")
	for _, want := range []string{"dt241m/device/+/channel/set", "dt241m/device/+/name/set", mqtt.ControllerRescanPress, mqtt.HAStatusTopic} {
		if !strings.Contains(subs, want) {
			t.Fatalf("missing subscription %s", want)
		}
	}
}

func TestMQTTCommandChangesReceiverChannel(t *testing.T) {
	h, r, _ := started(t)
	h.mqtt.Deliver(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelSet, "5")
	eventually(t, func() bool { return h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState) == "5" }, "state 5")
	if r.ReportedChannel() != 5 || len(h.net.WritesTo(rxIP)) != 1 {
		t.Fatal("write not applied exactly once")
	}
	if last, _ := h.mqtt.LastOn(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState); !last.Retain {
		t.Fatal("state should be retained")
	}
}

func TestInvalidCommandsAreIgnored(t *testing.T) {
	h, _, x := started(t)
	h.mqtt.Deliver(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelSet, "abc")
	h.mqtt.Deliver(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelSet, "300")
	h.mqtt.Deliver(mqtt.ForDevice(testutil.RxFixtureMAC).SourceSet, "No Such Transmitter")
	h.mqtt.Deliver(mqtt.ForDevice(testutil.TxFixtureMAC).SourceSet, "ET01_286CD291")
	h.mqtt.Deliver(mqtt.ControllerRescanPress, "PRESS")
	eventually(t, func() bool { return h.ctrl.DiscoveryRunCount() == 2 }, "rescan")
	if len(h.net.AllWrites()) != 0 || x.ReportedChannel() != 3 {
		t.Fatal("unexpected write")
	}
	for _, p := range h.mqtt.Published() {
		if strings.HasSuffix(p.Topic, "/set") || p.Topic == mqtt.ControllerRescanPress {
			t.Fatalf("published to a command topic: %s", p.Topic)
		}
	}
}

func TestReconnectAndBirthRepublishWithoutTouchingHardware(t *testing.T) {
	h, _, _ := started(t)
	for _, trigger := range []func(){
		func() { h.mqtt.SimulateDisconnect(); h.mqtt.SimulateConnect() },
		func() { h.mqtt.Deliver(mqtt.HAStatusTopic, "online") },
	} {
		h.mqtt.Clear()
		h.net.ResetRequests()
		trigger()
		eventually(t, func() bool { return len(h.mqtt.DiscoveryConfigs()) >= 11 }, "republish")
		if h.mqtt.LastPayload(mqtt.ControllerAvailability) != "online" ||
			h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState) != "2" ||
			h.mqtt.LastPayload(mqtt.ForDevice(testutil.TxFixtureMAC).ChannelState) != "3" ||
			h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).NameState) != "ER02_286CD6D8" {
			t.Fatal("state not republished")
		}
		if len(h.net.Requests()) != 0 {
			t.Fatal("republish touched the hardware")
		}
	}
	h.mqtt.Clear()
	h.mqtt.Deliver(mqtt.HAStatusTopic, "offline")
	if len(h.mqtt.Published()) != 0 {
		t.Fatal("HA offline should not trigger publishes")
	}
}

func TestShutdownPublishesOfflineAndRefusesCommands(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(testutil.RxFixtureMAC, rxIP, h.net)
	if err := h.ctrl.Start(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.ctrl.Stop(h.ctx)
	if last, _ := h.mqtt.LastOn(mqtt.ControllerAvailability); last.Payload != "offline" || !last.Retain || !h.mqtt.Ended {
		t.Fatalf("shutdown state %+v ended=%v", last, h.mqtt.Ended)
	}
	_, err := h.ctrl.RequestChannelChange(h.ctx, testutil.RxFixtureMAC, 4)
	var rejected *controller.Rejected
	if !errors.As(err, &rejected) || rejected.Reason != controller.ReasonShuttingDown {
		t.Fatalf("expected shutting_down, got %v", err)
	}
}
