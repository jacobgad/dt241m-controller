package controller_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/config"
	"github.com/jacobgad/dt241m-controller/internal/controller"
	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	mqttpkg "github.com/jacobgad/dt241m-controller/internal/mqtt"
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
	h := &harness{net: o.net, mqtt: o.mqtt, store: o.store, logs: &lockedBuffer{}, ctx: context.Background()}
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
		Client:           dt241m.NewHTTPClient(dt241m.Options{Transport: h.net, Timeout: opts.ProbeTimeout}),
		MQTT:             h.mqtt,
		Store:            h.store,
		Options:          opts,
		Log:              slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Origin:           mqttOrigin,
		ReadbackAttempts: 2,
		ReadbackDelay:    0,
	})
	return h
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
