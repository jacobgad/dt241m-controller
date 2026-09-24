package controller_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/controller"
	"github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/testutil"
)

func TestDHCPReuseNeverWritesToWrongDevice(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	a := rx(rxA, "192.168.1.5", h.net, func(o *testutil.DeviceOptions) { o.Overrides = map[string]any{"dev_name": "RX_A"} })
	h.discover(t)

	h.net.Move("192.168.1.5", "192.168.1.10")
	b := rx(rxB, "192.168.1.5", h.net, func(o *testutil.DeviceOptions) {
		o.Overrides = map[string]any{"dev_name": "RX_B"}
		o.Reported = intp(9)
	})
	h.net.ResetRequests()

	outcome, err := h.ctrl.RequestChannelChange(h.ctx, rxA, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.net.RequestsTo("192.168.1.5", "get_device_info_proav")) == 0 {
		t.Fatal("old IP was not verified")
	}
	if len(h.net.WritesTo("192.168.1.5")) != 0 || b.Writes() != 0 || b.ReportedChannel() != 9 {
		t.Fatal("wrote to the wrong device")
	}
	if len(h.net.WritesTo("192.168.1.10")) != 1 || a.ReportedChannel() != 7 {
		t.Fatal("did not write to relocated device")
	}
	if outcome.Status != controller.StatusMatched || outcome.IP != "192.168.1.10" {
		t.Fatalf("outcome %+v", outcome)
	}
	reqs := h.net.Requests()
	firstWrite, verifyNew := -1, -1
	for i, r := range reqs {
		if r.RPC == nil {
			continue
		}
		if r.RPC.Method == "set_channel_id" && firstWrite < 0 {
			firstWrite = i
		}
		if r.IP == "192.168.1.10" && r.RPC.Method == "get_device_info_proav" && verifyNew < 0 {
			verifyNew = i
		}
	}
	if verifyNew < 0 || verifyNew > firstWrite {
		t.Fatal("new IP was not verified before writing")
	}
	adapterA, _ := h.ctrl.Registry.Get(rxA)
	adapterB, _ := h.ctrl.Registry.Get(rxB)
	if adapterA.IP != "192.168.1.10" || adapterA.ID != "dt241m_aaaaaaaaaaaa" || adapterB.IP != "192.168.1.5" || len(h.ctrl.Registry.All()) != 2 {
		t.Fatal("registry wrong")
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(rxA).ChannelState) != "7" || h.mqtt.LastPayload(mqtt.ForDevice(rxB).ChannelState) != "9" {
		t.Fatal("published states wrong")
	}
	if !h.logs.Contains("identity_mismatch") {
		t.Fatal("identity_mismatch not logged")
	}
}

func TestNoWriteWhenTargetCannotBeLocated(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(rxA, "192.168.1.5", h.net)
	h.discover(t)
	h.net.Remove("192.168.1.5")
	b := rx(rxB, "192.168.1.5", h.net)

	outcome, err := h.ctrl.RequestChannelChange(h.ctx, rxA, 7)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != controller.StatusFailed || outcome.Reason != "device_not_located" || b.Writes() != 0 {
		t.Fatalf("outcome %+v writes %d", outcome, b.Writes())
	}
	a, _ := h.ctrl.Registry.Get(rxA)
	if a.Online {
		t.Fatal("should be offline")
	}
}

func TestNoWriteWhenStoredIPSilentAndDeviceGone(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(rxA, "192.168.1.5", h.net)
	h.discover(t)
	h.net.Remove("192.168.1.5")
	outcome, _ := h.ctrl.RequestChannelChange(h.ctx, rxA, 3)
	if outcome.Status != controller.StatusFailed || len(h.net.AllWrites()) != 0 {
		t.Fatal("unexpected write")
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(rxA).Availability) != "offline" {
		t.Fatal("not marked offline")
	}
}

func TestFollowsDeviceToNewIPWhenOldIsSilent(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	a := rx(rxA, "192.168.1.5", h.net)
	h.discover(t)
	h.net.Move("192.168.1.5", "192.168.1.13")
	outcome, _ := h.ctrl.RequestChannelChange(h.ctx, rxA, 3)
	if outcome.Status != controller.StatusMatched || a.ReportedChannel() != 3 || len(h.net.WritesTo("192.168.1.13")) != 1 {
		t.Fatalf("outcome %+v", outcome)
	}
	if h.mqtt.LastPayload(mqtt.ForDevice(rxA).Availability) != "online" {
		t.Fatal("should be back online")
	}
}

func TestRefusesTransmittersUnknownRolesAndUnknownDevices(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tx(testutil.TxFixtureMAC, "192.168.1.5", h.net)
	rx("cc:cc:cc:cc:cc:cc", "192.168.1.6", h.net, func(o *testutil.DeviceOptions) {
		o.Overrides = map[string]any{"product_name": "Mystery", "model": "unknown"}
	})
	h.discover(t)
	mystery, _ := h.ctrl.Registry.Get("cc:cc:cc:cc:cc:cc")
	if mystery.Role != "unknown" {
		t.Fatal("expected unknown role")
	}
	for macAddr, reason := range map[string]string{testutil.TxFixtureMAC: "not_a_receiver", "cc:cc:cc:cc:cc:cc": "not_a_receiver", "dd:dd:dd:dd:dd:dd": "unknown_device"} {
		_, err := h.ctrl.RequestChannelChange(h.ctx, macAddr, 2)
		var rej *controller.Rejected
		if !errors.As(err, &rej) || rej.Reason != reason {
			t.Fatalf("%s: got %v want %s", macAddr, err, reason)
		}
	}
	if len(h.net.AllWrites()) != 0 {
		t.Fatal("a write was sent")
	}
}

func gatedWrite(gate <-chan struct{}) testutil.Responder {
	return func(rpc *testutil.RPC, d *testutil.Device) *http.Response {
		if rpc == nil || rpc.Method != "set_channel_id" {
			return nil
		}
		channel := int(rpc.Params["channel_id"].(float64))
		<-gate
		d.ApplyChannel(channel)
		return testutil.DeviceResponse(`{"jsonrpc":"2.0","id":1,"result":{"result":true}}`)
	}
}

func TestSameReceiverCommandsExecuteInOrder(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	d := rx(rxA, "192.168.1.5", h.net)
	h.discover(t)
	gate := make(chan struct{})
	once := true
	d.Responder = func(rpc *testutil.RPC, dev *testutil.Device) *http.Response {
		if rpc == nil || rpc.Method != "set_channel_id" {
			return nil
		}
		if once {
			once = false
			return gatedWrite(gate)(rpc, dev)
		}
		dev.ApplyChannel(int(rpc.Params["channel_id"].(float64)))
		return testutil.DeviceResponse(`{"jsonrpc":"2.0","id":1,"result":{"result":true}}`)
	}

	results := make(chan controller.Outcome, 2)
	go func() { o, _ := h.ctrl.RequestChannelChange(h.ctx, rxA, 2); results <- o }()
	eventually(t, func() bool { return len(h.net.WritesTo("192.168.1.5")) == 1 }, "first write to start")
	go func() { o, _ := h.ctrl.RequestChannelChange(h.ctx, rxA, 5); results <- o }()
	time.Sleep(30 * time.Millisecond)
	if len(h.net.WritesTo("192.168.1.5")) != 1 {
		t.Fatal("second write started before first completed")
	}
	close(gate)
	first, second := <-results, <-results
	if first.Status != controller.StatusMatched || second.Status != controller.StatusMatched {
		t.Fatalf("outcomes %+v %+v", first, second)
	}
	writes := h.net.WritesTo("192.168.1.5")
	if len(writes) != 2 || writes[0].ChannelParam() != 2 || writes[1].ChannelParam() != 5 {
		t.Fatalf("write order %+v", writes)
	}
	if d.ReportedChannel() != 5 || h.mqtt.LastPayload(mqtt.ForDevice(rxA).ChannelState) != "5" {
		t.Fatal("final state should be the newest request")
	}
	states := h.mqtt.PayloadsOn(mqtt.ForDevice(rxA).ChannelState)
	if lastIndex(states, "2") > lastIndex(states, "5") {
		t.Fatal("older readback published after newer")
	}
}

func TestDifferentReceiversRunConcurrently(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	a := rx(rxA, "192.168.1.5", h.net)
	b := rx(rxB, "192.168.1.6", h.net)
	h.discover(t)
	gate := make(chan struct{})
	a.Responder = gatedWrite(gate)

	first := make(chan controller.Outcome, 1)
	go func() { o, _ := h.ctrl.RequestChannelChange(h.ctx, rxA, 3); first <- o }()
	eventually(t, func() bool { return len(h.net.WritesTo("192.168.1.5")) == 1 }, "A's write to start")
	second, _ := h.ctrl.RequestChannelChange(h.ctx, rxB, 4)
	if second.Status != controller.StatusMatched || b.ReportedChannel() != 4 || a.ReportedChannel() != 2 {
		t.Fatal("B should complete while A is blocked")
	}
	close(gate)
	if o := <-first; o.Status != controller.StatusMatched || a.ReportedChannel() != 3 {
		t.Fatalf("A outcome %+v", o)
	}
}

func lastIndex(list []string, v string) int {
	for i := len(list) - 1; i >= 0; i-- {
		if list[i] == v {
			return i
		}
	}
	return -1
}

func TestStaleFrontPanelIsNotAnError(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	d := rx(testutil.RxFixtureMAC, "192.168.1.5", h.net, func(o *testutil.DeviceOptions) { o.Reported = intp(3); o.FrontPanel = intp(3) })
	h.discover(t)
	outcome, _ := h.ctrl.RequestChannelChange(h.ctx, testutil.RxFixtureMAC, 2)
	if d.ReportedChannel() != 2 || d.Video != 2 || d.FrontPanel != 3 {
		t.Fatalf("device %+v", d)
	}
	if outcome.Status != controller.StatusMatched || h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState) != "2" {
		t.Fatalf("outcome %+v", outcome)
	}
	if h.logs.Contains("channel_readback_mismatch") || h.logs.Contains("FrontPanel") || h.logs.Contains("front_panel") {
		t.Fatal("front panel leaked into behaviour")
	}
}

func TestAckWithoutApplyIsMismatchWithoutRetry(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	d := rx(testutil.RxFixtureMAC, "192.168.1.5", h.net, func(o *testutil.DeviceOptions) { o.AckWithoutApply = true })
	h.discover(t)
	h.mqtt.Clear()
	outcome, _ := h.ctrl.RequestChannelChange(h.ctx, testutil.RxFixtureMAC, 5)
	if outcome.Status != controller.StatusMismatch || *outcome.Reported != 2 || d.Writes() != 1 {
		t.Fatalf("outcome %+v writes %d", outcome, d.Writes())
	}
	for _, p := range h.mqtt.PayloadsOn(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState) {
		if p == "5" {
			t.Fatal("requested value published optimistically")
		}
	}
	if !h.logs.Contains("channel_readback_mismatch") {
		t.Fatal("mismatch not logged")
	}
}

func TestWriteTimeoutIsAmbiguousAndNotResent(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	d := rx(testutil.RxFixtureMAC, "192.168.1.5", h.net, func(o *testutil.DeviceOptions) { o.WriteHangs = true })
	h.discover(t)
	outcome, _ := h.ctrl.RequestChannelChange(h.ctx, testutil.RxFixtureMAC, 4)
	if d.Writes() != 1 || len(h.net.WritesTo("192.168.1.5")) != 1 {
		t.Fatal("write was resent")
	}
	if outcome.Status != controller.StatusMatched || outcome.WriteAccepted != "unknown" || h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState) != "4" {
		t.Fatalf("outcome %+v", outcome)
	}
	if !h.logs.Contains("channel_change_ambiguous") {
		t.Fatal("ambiguity not logged")
	}
}

func TestRejectedWritePublishesActualState(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(testutil.RxFixtureMAC, "192.168.1.5", h.net, func(o *testutil.DeviceOptions) { o.RejectWrites = true })
	h.discover(t)
	outcome, _ := h.ctrl.RequestChannelChange(h.ctx, testutil.RxFixtureMAC, 4)
	if outcome.Status != controller.StatusFailed || outcome.Reason != "write_rejected" || *outcome.Reported != 2 {
		t.Fatalf("outcome %+v", outcome)
	}
}

func TestChannelZeroIsReal(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	d := rx(testutil.RxFixtureMAC, "192.168.1.5", h.net)
	h.discover(t)
	outcome, _ := h.ctrl.RequestChannelChange(h.ctx, testutil.RxFixtureMAC, 0)
	if outcome.Status != controller.StatusMatched || d.ReportedChannel() != 0 || h.mqtt.LastPayload(mqtt.ForDevice(testutil.RxFixtureMAC).ChannelState) != "0" {
		t.Fatalf("outcome %+v", outcome)
	}
}

func TestParsesHTMLLabelledJSON(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	rx(testutil.RxFixtureMAC, "192.168.1.5", h.net, func(o *testutil.DeviceOptions) {
		o.Responder = func(rpc *testutil.RPC, _ *testutil.Device) *http.Response {
			if rpc != nil && rpc.Method == "get_device_info_proav" {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(testutil.FixtureText("rx-info-initial-channel-2")))}
			}
			return nil
		}
	})
	h.discover(t)
	if _, ok := h.ctrl.Registry.Get(testutil.RxFixtureMAC); !ok {
		t.Fatal("device not discovered from text/html response")
	}
}
