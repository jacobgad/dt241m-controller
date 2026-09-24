package controller_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/config"
	"github.com/jacobgad/dt241m-controller/internal/controller"
	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	mqttpkg "github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/registry"
	"github.com/jacobgad/dt241m-controller/internal/store"
	"github.com/jacobgad/dt241m-controller/internal/testutil"
)

const testRange = "192.168.1.0/28"

type harness struct {
	net   *testutil.Network
	mqtt  *testutil.FakeMQTT
	store store.Store
	ctrl  *controller.Controller
	logs  *lockedBuffer
	ctx   context.Context
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Contains(s string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Contains(b.buf.String(), s)
}

type harnessOptions struct {
	net   *testutil.Network
	mqtt  *testutil.FakeMQTT
	store store.Store
}

func newHarness(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	h := &harness{net: o.net, mqtt: o.mqtt, store: o.store, logs: &lockedBuffer{}, ctx: t.Context()}
	if h.net == nil {
		h.net = testutil.NewNetwork()
	}
	if h.mqtt == nil {
		h.mqtt = testutil.NewFakeMQTT(true)
	}
	if h.store == nil {
		h.store = testutil.NewMemoryStore()
	}
	opts, err := config.ParseOptions([]byte(`{"scan_ranges":["` + testRange + `"],"poll_interval_seconds":5,"probe_timeout_ms":200,"discovery_concurrency":4,"log_level":"debug"}`))
	if err != nil {
		t.Fatal(err)
	}
	h.ctrl = controller.New(controller.Deps{
		Client:   dt241m.NewHTTPClient(dt241m.Options{Transport: h.net, Timeout: opts.ProbeTimeout}),
		MQTT:     h.mqtt,
		Store:    h.store,
		Options:  opts,
		Log:      slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Origin:   mqttOrigin,
		Readback: controller.Readback{Attempts: 2},
	})
	return h
}

// change requests a channel change and waits for its outcome.
func (h *harness) change(t *testing.T, mac string, channel int) controller.Outcome {
	t.Helper()
	result, err := h.ctrl.ChangeChannel(mac, channel)
	if err != nil {
		t.Fatalf("change %s -> %d: %v", mac, channel, err)
	}
	return <-result
}

// selectSource requests a source change and waits for its outcome.
func (h *harness) selectSource(t *testing.T, mac, label string) controller.Outcome {
	t.Helper()
	result, err := h.ctrl.ChangeSource(mac, label)
	if err != nil {
		t.Fatalf("select %s -> %q: %v", mac, label, err)
	}
	return <-result
}

func (h *harness) adapter(t *testing.T, mac string) registry.Adapter {
	t.Helper()
	a, ok := h.ctrl.Adapter(mac)
	if !ok {
		t.Fatalf("adapter %s unknown", mac)
	}
	return a
}

func rejectedWith(t *testing.T, err error, reason controller.FailureReason) {
	t.Helper()
	var rejected *controller.RejectedError
	if !errors.As(err, &rejected) || rejected.Reason != reason {
		t.Fatalf("expected rejection %s, got %v", reason, err)
	}
}

func (h *harness) discover(t *testing.T) {
	t.Helper()
	h.ctrl.RunDiscovery(h.ctx, "test")
}

func rx(mac, ip string, net *testutil.Network, opts ...func(*testutil.DeviceOptions)) *testutil.Device {
	o := testutil.DeviceOptions{MAC: mac, Fixture: "rx-info-initial-channel-2"}
	for _, apply := range opts {
		apply(&o)
	}
	return net.Place(ip, testutil.NewDevice(o))
}

func tx(mac, ip string, net *testutil.Network) *testutil.Device {
	return net.Place(ip, testutil.NewDevice(testutil.DeviceOptions{MAC: mac, Fixture: "tx-info-initial-channel-3"}))
}

func eventually(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func intp(i int) *int { return &i }

var mqttOrigin = mqttpkg.Origin{Version: "test", SupportURL: "https://example.invalid"}
