// Package controller orchestrates the add-on: discovery and polling of DT241M units,
// the per-device write queue with identity verification, and publication of the
// resulting state to Home Assistant over MQTT.
//
// The package is split by responsibility: controller.go owns the lifecycle and wiring,
// discovery.go everything that reads devices, write.go everything that changes them,
// and publish.go the translation of registry state into MQTT.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/config"
	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/registry"
	"github.com/jacobgad/dt241m-controller/internal/store"
)

const mqttStartupWait = 10 * time.Second

// Controller is the add-on's long-lived core. Create it with New and drive it with Start/Stop.
type Controller struct {
	client   dt241m.Client
	mqtt     mqtt.Connection
	store    store.Store
	opts     config.Options
	log      *slog.Logger
	now      func() time.Time
	readback Readback
	registry *registry.Registry
	pub      *publisher
	queue    *keyedQueue

	// lifetime spans New to Stop. It is the one context this type owns: broker callbacks
	// arrive with no context of their own and may fire before Start.
	lifetime context.Context
	endLife  context.CancelFunc
	inflight sync.WaitGroup

	discoveryMu   sync.Mutex
	discoveryDone chan struct{}
	discoveryRuns atomic.Int64

	started  atomic.Bool
	pollDone chan struct{}
}

// New wires the controller to its MQTT connection; nothing talks to hardware until Start.
func New(deps Deps) *Controller {
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	readback := deps.Readback
	if readback.Attempts <= 0 {
		readback = DefaultReadback
	}
	reg := registry.New()
	c := &Controller{
		client:   deps.Client,
		mqtt:     deps.MQTT,
		store:    deps.Store,
		opts:     deps.Options,
		log:      log,
		now:      now,
		readback: readback,
		registry: reg,
		pub:      &publisher{conn: deps.MQTT, registry: reg, origin: deps.Origin, log: log},
		queue:    newKeyedQueue(),
		pollDone: make(chan struct{}),
	}
	c.lifetime, c.endLife = context.WithCancel(context.Background())

	c.mqtt.OnMessage(mqtt.NewRouter(mqtt.Actions{
		ChannelCommand: func(m string, channel int) {
			if _, err := c.ChangeChannel(m, channel); err != nil {
				c.log.Error("operation_failed", "mac", m, "requestedChannel", channel, "error", err)
			}
		},
		SourceCommand: func(m, label string) {
			if _, err := c.ChangeSource(m, label); err != nil {
				c.log.Error("operation_failed", "mac", m, "requestedSource", label, "error", err)
			}
		},
		NameCommand: func(m, raw string) {
			c.background(func(ctx context.Context) {
				if _, err := c.Rename(ctx, m, raw); err != nil {
					c.log.Warn("rename_failed", "mac", m, "error", err)
				}
			})
		},
		RescanRequested:     func() { c.background(func(ctx context.Context) { c.RunDiscovery(ctx, "manual_rescan") }) },
		HomeAssistantOnline: func() { c.background(c.pub.everything) },
	}, c.log))
	c.mqtt.OnConnect(func() { c.background(c.onMQTTConnected) })
	return c
}

// background runs fn under the controller's lifetime and lets Stop wait for it.
func (c *Controller) background(fn func(context.Context)) {
	c.inflight.Add(1)
	go func() {
		defer c.inflight.Done()
		fn(c.lifetime)
	}()
}

func (c *Controller) stopping() bool {
	return c.lifetime.Err() != nil
}

// Adapter returns the current view of one adapter by normalised MAC.
func (c *Controller) Adapter(macAddr string) (registry.Adapter, bool) {
	return c.registry.Lookup(macAddr)
}

// Adapters returns every known adapter ordered by MAC.
func (c *Controller) Adapters() []registry.Adapter {
	return c.registry.All()
}

// Start loads the persisted inventory, publishes it as unavailable, re-probes last-known
// addresses, runs the first full scan and begins polling. It returns once the scan is done.
func (c *Controller) Start(ctx context.Context) error {
	if !c.started.CompareAndSwap(false, true) {
		return nil
	}
	c.log.Info("controller_started",
		"scanRanges", strings.Join(c.opts.ScanRangeTexts(), ","),
		"pollInterval", c.opts.PollInterval,
		"probeTimeout", c.opts.ProbeTimeout,
		"discoveryConcurrency", c.opts.DiscoveryConcurrency)

	persisted, err := c.store.LoadAll(ctx)
	if err != nil {
		return fmt.Errorf("load adapters: %w", err)
	}
	c.registry.Hydrate(persisted)
	c.log.Info("adapters_loaded", "count", len(persisted))

	waitCtx, cancel := context.WithTimeout(ctx, mqttStartupWait)
	err = c.mqtt.AwaitConnection(waitCtx)
	cancel()
	if err != nil {
		c.log.Warn("mqtt_not_ready", "detail", "continuing; state will be republished on connect")
	} else {
		c.onMQTTConnected(ctx)
	}

	c.ProbeKnownAddresses(ctx)
	c.RunDiscovery(ctx, "startup")

	go c.pollLoop()
	return nil
}

// Stop ends background work, publishes the controller offline and closes MQTT.
// It gives up waiting when ctx expires so a stuck device cannot block shutdown.
func (c *Controller) Stop(ctx context.Context) {
	c.endLife()
	if c.started.Load() {
		waitFor(ctx, c.pollDone)
	}
	c.discoveryMu.Lock()
	done := c.discoveryDone
	c.discoveryMu.Unlock()
	if done != nil {
		waitFor(ctx, done)
	}
	waitFor(ctx, whenDone(func() { c.queue.wait(); c.inflight.Wait() }))
	if c.mqtt.Connected() {
		c.pub.controllerOffline(ctx)
	}
	if err := c.mqtt.Close(ctx); err != nil {
		c.log.Warn("operation_failed", "operation", "mqtt_close", "error", err)
	}
	c.log.Info("controller_stopped")
}

func waitFor(ctx context.Context, done <-chan struct{}) {
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func whenDone(fn func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}

func (c *Controller) pollLoop() {
	defer close(c.pollDone)
	ticker := time.NewTicker(c.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.lifetime.Done():
			return
		case <-ticker.C:
			c.PollKnownDevices(c.lifetime)
		}
	}
}

func (c *Controller) onMQTTConnected(ctx context.Context) {
	if err := c.mqtt.Subscribe(ctx, mqtt.Subscriptions); err != nil {
		c.log.Error("operation_failed", "operation", "subscribe", "error", err)
	}
	c.pub.everything(ctx)
}

// Rename stores a user-chosen name for an adapter and republishes its discovery. The
// hardware is never contacted; reportedName is left untouched.
func (c *Controller) Rename(ctx context.Context, macAddr, raw string) (registry.Adapter, error) {
	adapter, ok := c.registry.Lookup(normalize(macAddr))
	if !ok {
		c.log.Warn("rename_unknown_device", "mac", macAddr)
		return registry.Adapter{}, &RejectedError{Reason: ReasonUnknownDevice}
	}
	name, err := registry.ValidateName(raw)
	if err != nil {
		c.log.Warn("rename_rejected", "mac", adapter.MAC, "reason", err)
		c.pub.rejectedName(ctx, adapter)
		return adapter, &RejectedError{Reason: ReasonInvalidName, Detail: err.Error()}
	}
	previous := adapter.Name
	updated, _ := c.registry.SetName(adapter.MAC, name)
	if err := c.store.SetName(ctx, adapter.MAC, name); err != nil {
		c.log.Error("persist_failed", "mac", adapter.MAC, "error", err)
	}
	c.log.Info("adapter_renamed", "mac", adapter.MAC, "previousName", strOrNil(previous), "name", strOrNil(name), "reportedName", adapter.ReportedName)
	c.pub.renamed(ctx, updated)
	return updated, nil
}

func strOrNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func intOrNil(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}
