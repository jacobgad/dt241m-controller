// Package controller orchestrates the add-on: discovery and polling of DT241M units,
// the per-device write queue with identity verification, and publication of the
// resulting state to Home Assistant over MQTT.
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
	"github.com/jacobgad/dt241m-controller/internal/mac"
	"github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/registry"
	"github.com/jacobgad/dt241m-controller/internal/store"
)

// Readback bounds how long a write waits for the device to report the new channel.
type Readback struct {
	Attempts int
	Gap      time.Duration
}

// DefaultReadback is used when Deps.Readback is zero.
var DefaultReadback = Readback{Attempts: 3, Gap: 400 * time.Millisecond}

// Deps wires a Controller. Zero values for Log, Now and Readback pick defaults.
type Deps struct {
	Client   dt241m.Client
	MQTT     mqtt.Connection
	Store    store.Store
	Options  config.Options
	Log      *slog.Logger
	Origin   mqtt.Origin
	Now      func() time.Time
	Readback Readback
}

// Status is the result class of a channel change.
type Status string

// Channel change statuses.
const (
	StatusMatched  Status = "matched"
	StatusMismatch Status = "mismatch"
	StatusFailed   Status = "failed"
)

// WriteStatus records what the device said about the write itself, independent of readback.
type WriteStatus string

// Write acknowledgement states.
const (
	WriteAccepted  WriteStatus = "accepted"
	WriteRejected  WriteStatus = "rejected"
	WriteAmbiguous WriteStatus = "ambiguous"
)

// FailureReason explains a StatusFailed outcome or a RejectedError.
type FailureReason string

// Failure reasons.
const (
	ReasonDeviceNotLocated FailureReason = "device_not_located"
	ReasonUnknownRole      FailureReason = "unknown_role"
	ReasonUnknownSource    FailureReason = "unknown_source"
	ReasonWriteRejected    FailureReason = "write_rejected"
	ReasonInvalidChannel   FailureReason = "invalid_channel"
	ReasonUnknownDevice    FailureReason = "unknown_device"
	ReasonShuttingDown     FailureReason = "shutting_down"
)

// Outcome is the fully observed result of a channel change: what was asked, what the
// device acknowledged, and what it reported afterwards.
type Outcome struct {
	Status    Status
	MAC       string
	IP        string
	Requested int
	Reported  *int
	Reason    FailureReason
	Write     WriteStatus
}

// RejectedError is returned when a request is refused before anything is sent.
type RejectedError struct {
	Reason FailureReason
}

func (e *RejectedError) Error() string { return "request rejected: " + string(e.Reason) }

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
	inflight sync.WaitGroup

	lifetime context.Context
	endLife  context.CancelFunc

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

// background runs fn under the controller's lifetime context and lets Stop wait for it.
// The lifetime is the one context this type owns: it spans New to Stop and is what
// broker callbacks, which arrive with no context of their own, run under.
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

// DiscoveryRunCount is the number of full scans started so far.
func (c *Controller) DiscoveryRunCount() int { return int(c.discoveryRuns.Load()) }

// DiscoveryRunning reports whether a full scan is in progress.
func (c *Controller) DiscoveryRunning() bool {
	c.discoveryMu.Lock()
	defer c.discoveryMu.Unlock()
	return c.discoveryDone != nil
}

func (c *Controller) pollLoop() {
	defer close(c.pollDone)
	ctx := c.lifetime
	ticker := time.NewTicker(c.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.PollKnownDevices(ctx)
		}
	}
}

func (c *Controller) onMQTTConnected(ctx context.Context) {
	if err := c.mqtt.Subscribe(ctx, mqtt.Subscriptions); err != nil {
		c.log.Error("operation_failed", "operation", "subscribe", "error", err)
	}
	c.pub.everything(ctx)
}

// PublishEverything re-sends availability, discovery configs and last known state for
// the controller and every adapter. It never contacts the hardware.
func (c *Controller) PublishEverything(ctx context.Context) {
	c.pub.everything(ctx)
}

// RunDiscovery scans every configured range. If a scan is already running the call
// waits for it (or for ctx) rather than starting a second one.
func (c *Controller) RunDiscovery(ctx context.Context, reason string) {
	c.discoveryMu.Lock()
	if c.discoveryDone != nil {
		done := c.discoveryDone
		c.discoveryMu.Unlock()
		c.log.Debug("discovery_already_running", "reason", reason)
		waitFor(ctx, done)
		return
	}
	if c.stopping() {
		c.discoveryMu.Unlock()
		return
	}
	done := make(chan struct{})
	c.discoveryDone = done
	c.discoveryMu.Unlock()

	c.executeDiscovery(ctx, reason)

	c.discoveryMu.Lock()
	c.discoveryDone = nil
	c.discoveryMu.Unlock()
	close(done)
}

func (c *Controller) executeDiscovery(ctx context.Context, reason string) {
	targets := c.opts.ScanHosts()
	c.discoveryRuns.Add(1)
	c.log.Info("discovery_started", "reason", reason, "addresses", len(targets), "ranges", strings.Join(c.opts.ScanRangeTexts(), ","))
	started := c.now()
	var found atomic.Int64
	Probe(ctx, c.client, targets, ProbeOptions{
		Concurrency: c.opts.DiscoveryConcurrency,
		Timeout:     c.opts.ProbeTimeout,
		OnHit: func(hit Hit) {
			found.Add(1)
			c.applyObservation(ctx, hit.IP, hit.Info)
		},
	})
	c.pub.counts(ctx, false)
	c.log.Info("discovery_completed", "reason", reason, "found", found.Load(), "known", c.registry.Counts().Known, "duration", c.now().Sub(started))
}

// ProbeKnownAddresses re-checks every persisted adapter at its last-known IP.
func (c *Controller) ProbeKnownAddresses(ctx context.Context) {
	var candidates []registry.Adapter
	for _, a := range c.registry.All() {
		if a.IP != "" {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		return
	}
	c.log.Info("known_addresses_probe_started", "count", len(candidates))
	c.forEachAdapter(candidates, func(a registry.Adapter) {
		c.observeAt(ctx, a.IP, a.MAC)
	})
}

// PollKnownDevices observes every adapter once. It only reads; a device that has gone
// quiet is marked offline and, if it was online before, triggers one full rescan.
func (c *Controller) PollKnownDevices(ctx context.Context) {
	if c.stopping() {
		return
	}
	adapters := c.registry.All()
	if len(adapters) == 0 {
		return
	}
	var needsRediscovery atomic.Bool
	c.forEachAdapter(adapters, func(a registry.Adapter) {
		if a.IP == "" {
			return
		}
		switch c.observeAt(ctx, a.IP, a.MAC) {
		case observeVerified:
		case observeOtherDevice:
			needsRediscovery.Store(true)
		case observeUnreachable:
			c.markOffline(ctx, a.MAC)
			if a.Online {
				needsRediscovery.Store(true)
			}
		}
	})
	if needsRediscovery.Load() && !c.stopping() {
		c.RunDiscovery(ctx, "device_disappeared")
	}
}

func (c *Controller) forEachAdapter(adapters []registry.Adapter, fn func(registry.Adapter)) {
	sem := make(chan struct{}, c.opts.DiscoveryConcurrency)
	var wg sync.WaitGroup
	for _, a := range adapters {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(a)
		}()
	}
	wg.Wait()
}

// ChangeChannel validates a request and claims its place in the device's queue before
// returning, so calls made in order are executed in order. The outcome arrives on the
// returned channel once the write and readback have completed.
func (c *Controller) ChangeChannel(macAddr string, channel int) (<-chan Outcome, error) {
	if c.stopping() {
		return nil, &RejectedError{Reason: ReasonShuttingDown}
	}
	if !dt241m.ValidChannel(channel) {
		return nil, &RejectedError{Reason: ReasonInvalidChannel}
	}
	adapter, ok := c.registry.Lookup(mac.Normalize(macAddr))
	if !ok {
		c.log.Warn("channel_command_unknown_device", "mac", macAddr)
		return nil, &RejectedError{Reason: ReasonUnknownDevice}
	}
	if !writableRole(adapter.Role) {
		c.log.Warn("channel_command_refused_role", "mac", adapter.MAC, "role", adapter.Role, "requestedChannel", channel)
		return nil, &RejectedError{Reason: ReasonUnknownRole}
	}
	c.log.Info("channel_change_requested", "mac", adapter.MAC, "ip", adapter.IP, "role", adapter.Role, "requestedChannel", channel)
	result := make(chan Outcome, 1)
	c.queue.enqueue(adapter.MAC, func() {
		result <- c.executeChannelChange(c.lifetime, adapter.MAC, channel)
	})
	return result, nil
}

// ChangeSource tunes a receiver to the transmitter currently carrying the given Source label.
func (c *Controller) ChangeSource(macAddr, label string) (<-chan Outcome, error) {
	adapter, ok := c.registry.Lookup(mac.Normalize(macAddr))
	if !ok {
		c.log.Warn("source_command_unknown_device", "mac", macAddr)
		return nil, &RejectedError{Reason: ReasonUnknownDevice}
	}
	if adapter.Role != dt241m.RoleReceiver {
		c.log.Warn("source_command_refused_role", "mac", adapter.MAC, "role", adapter.Role)
		return nil, &RejectedError{Reason: ReasonUnknownRole}
	}
	source, ok := c.registry.Sources().ByLabel(label)
	if !ok {
		c.log.Warn("source_command_invalid", "mac", adapter.MAC, "requestedSource", label)
		return nil, &RejectedError{Reason: ReasonUnknownSource}
	}
	c.log.Info("source_change_requested", "mac", adapter.MAC, "requestedSource", label, "transmitterMac", source.MAC, "channel", source.Channel)
	return c.ChangeChannel(adapter.MAC, source.Channel)
}

func writableRole(role dt241m.Role) bool {
	return role == dt241m.RoleReceiver || role == dt241m.RoleTransmitter
}

func (c *Controller) executeChannelChange(ctx context.Context, macAddr string, requested int) Outcome {
	ip, ok := c.resolveVerifiedIP(ctx, macAddr)
	if !ok {
		c.log.Error("operation_failed", "mac", macAddr, "requestedChannel", requested, "reason", ReasonDeviceNotLocated)
		return Outcome{Status: StatusFailed, MAC: macAddr, Requested: requested, Reason: ReasonDeviceNotLocated}
	}
	adapter, _ := c.registry.Lookup(macAddr)
	if !writableRole(adapter.Role) {
		c.log.Warn("channel_command_refused_role", "mac", macAddr, "role", adapter.Role, "requestedChannel", requested)
		return Outcome{Status: StatusFailed, MAC: macAddr, IP: ip, Requested: requested, Reason: ReasonUnknownRole, Reported: adapter.Channel}
	}

	write := WriteAccepted
	writeCtx, cancel := context.WithTimeout(ctx, c.opts.ProbeTimeout)
	err := c.client.SetChannel(writeCtx, ip, requested)
	cancel()
	switch {
	case err == nil:
		c.log.Info("channel_change_acknowledged", "mac", macAddr, "ip", ip, "requestedChannel", requested)
	case dt241m.Ambiguous(err):
		write = WriteAmbiguous
		c.log.Warn("channel_change_ambiguous", "mac", macAddr, "ip", ip, "requestedChannel", requested, "error", err)
	default:
		write = WriteRejected
		c.log.Error("operation_failed", "mac", macAddr, "ip", ip, "requestedChannel", requested, "error", err)
	}

	reported := c.readbackChannel(ctx, macAddr, ip, requested)
	outcome := Outcome{MAC: macAddr, IP: ip, Requested: requested, Reported: reported, Write: write}
	switch {
	case write == WriteRejected:
		outcome.Status, outcome.Reason = StatusFailed, ReasonWriteRejected
	case reported != nil && *reported == requested:
		outcome.Status = StatusMatched
		c.log.Info("channel_readback_matched", "mac", macAddr, "ip", ip, "requestedChannel", requested, "reportedChannel", *reported)
	default:
		outcome.Status = StatusMismatch
		c.log.Warn("channel_readback_mismatch", "mac", macAddr, "ip", ip, "requestedChannel", requested, "reportedChannel", intOrNil(reported), "write", write)
	}
	return outcome
}

func (c *Controller) readbackChannel(ctx context.Context, macAddr, ip string, requested int) *int {
	var reported *int
	for attempt := 1; attempt <= c.readback.Attempts; attempt++ {
		if attempt > 1 && c.readback.Gap > 0 {
			select {
			case <-time.After(c.readback.Gap):
			case <-ctx.Done():
				return reported
			}
		}
		readCtx, cancel := context.WithTimeout(ctx, c.opts.ProbeTimeout)
		info, err := c.client.GetDeviceInfo(readCtx, ip)
		cancel()
		if err != nil {
			c.log.Warn("channel_readback_unavailable", "mac", macAddr, "ip", ip, "attempt", attempt, "error", err)
			continue
		}
		if observed := mac.Normalize(info.LanMAC); observed != macAddr {
			c.log.Error("identity_mismatch", "mac", macAddr, "ip", ip, "observedMac", observed, "phase", "readback")
			c.applyObservation(ctx, ip, info)
			return nil
		}
		c.applyObservation(ctx, ip, info)
		channel := info.ChannelID
		reported = &channel
		if channel == requested {
			return reported
		}
	}
	return reported
}

// resolveVerifiedIP returns an address that has just been proven to belong to macAddr,
// rediscovering the device if its last-known address is silent or answers as someone else.
func (c *Controller) resolveVerifiedIP(ctx context.Context, macAddr string) (string, bool) {
	adapter, ok := c.registry.Lookup(macAddr)
	if !ok {
		return "", false
	}
	if adapter.IP != "" {
		outcome := c.observeAt(ctx, adapter.IP, macAddr)
		if outcome == observeVerified {
			return adapter.IP, true
		}
		if outcome == observeUnreachable {
			c.markOffline(ctx, macAddr)
		}
		c.log.Info("rediscovery_for_write", "mac", macAddr, "staleIp", adapter.IP, "reason", outcome)
	}
	c.RunDiscovery(ctx, "locate_"+macAddr)
	located, ok := c.registry.Lookup(macAddr)
	if ok && located.Online {
		return located.IP, true
	}
	c.log.Error("device_not_located", "mac", macAddr, "lastKnownIp", adapter.IP)
	return "", false
}

type observeOutcome string

const (
	observeVerified    observeOutcome = "verified"
	observeOtherDevice observeOutcome = "other_device"
	observeUnreachable observeOutcome = "unreachable"
)

func (c *Controller) observeAt(ctx context.Context, ip, expectedMAC string) observeOutcome {
	readCtx, cancel := context.WithTimeout(ctx, c.opts.ProbeTimeout)
	info, err := c.client.GetDeviceInfo(readCtx, ip)
	cancel()
	if err != nil {
		return observeUnreachable
	}
	observed := mac.Normalize(info.LanMAC)
	if observed != expectedMAC {
		c.log.Warn("identity_mismatch", "mac", expectedMAC, "ip", ip, "observedMac", observed)
		c.applyObservation(ctx, ip, info)
		return observeOtherDevice
	}
	c.applyObservation(ctx, ip, info)
	return observeVerified
}

func (c *Controller) markOffline(ctx context.Context, macAddr string) {
	offline, changed := c.registry.MarkOffline(macAddr)
	if !changed {
		return
	}
	c.log.Warn("adapter_offline", "mac", offline.MAC, "ip", offline.IP, "role", offline.Role)
	c.pub.availability(ctx, offline)
	c.pub.counts(ctx, false)
	c.persist(ctx, offline)
}

func (c *Controller) applyObservation(ctx context.Context, ip string, info *dt241m.DeviceInfo) {
	obs, ok := c.registry.RecordObservation(ip, info, c.now())
	if !ok {
		c.log.Warn("adapter_invalid_mac", "ip", ip, "reportedMac", info.LanMAC)
		return
	}
	a := obs.Adapter
	if obs.Displaced != nil {
		c.log.Warn("adapter_ip_taken_over", "mac", obs.Displaced.MAC, "ip", ip, "byMac", a.MAC)
		c.pub.availability(ctx, *obs.Displaced)
	}
	switch {
	case obs.Created:
		c.log.Info("adapter_discovered", "mac", a.MAC, "ip", ip, "role", a.Role, "reportedName", a.ReportedName, "channel", intOrNil(a.Channel))
	case obs.CameOnline:
		c.log.Info("adapter_online", "mac", a.MAC, "ip", ip, "role", a.Role)
	}
	if obs.PreviousIP != "" {
		c.log.Info("adapter_ip_changed", "mac", a.MAC, "previousIp", obs.PreviousIP, "ip", ip, "role", a.Role)
	}
	if obs.StateChanged && !obs.Created {
		c.log.Info("adapter_state_observed", "mac", a.MAC, "ip", ip, "role", a.Role, "reportedChannel", intOrNil(a.Channel))
	}

	table := c.registry.Sources()
	if obs.Created || obs.MetadataChanged {
		c.pub.discovery(ctx, a, table)
		c.pub.name(ctx, a)
	}
	if obs.CameOnline {
		c.pub.availability(ctx, a)
	}
	if obs.Created || obs.StateChanged || obs.RoleChanged {
		c.pub.state(ctx, a, table)
	}
	if obs.CameOnline || obs.Displaced != nil {
		c.pub.counts(ctx, false)
	}
	if obs.Changed() {
		c.persist(ctx, a)
	}
	if a.Role == dt241m.RoleTransmitter || obs.RoleChanged {
		c.pub.refreshSources(ctx)
	}
}

// Rename stores a user-chosen name for an adapter and republishes its discovery. The
// hardware is never contacted; reportedName is left untouched.
func (c *Controller) Rename(ctx context.Context, macAddr, raw string) (registry.Adapter, error) {
	adapter, ok := c.registry.Lookup(mac.Normalize(macAddr))
	if !ok {
		c.log.Warn("rename_unknown_device", "mac", macAddr)
		return registry.Adapter{}, &RejectedError{Reason: ReasonUnknownDevice}
	}
	name, err := registry.ValidateName(raw)
	if err != nil {
		c.log.Warn("rename_rejected", "mac", adapter.MAC, "reason", err)
		c.pub.name(ctx, adapter)
		return adapter, err
	}
	previous := adapter.Name
	updated, _ := c.registry.SetName(adapter.MAC, name)
	if err := c.store.SetName(ctx, adapter.MAC, name); err != nil {
		c.log.Error("persist_failed", "mac", adapter.MAC, "error", err)
	}
	c.log.Info("adapter_renamed", "mac", adapter.MAC, "previousName", strOrNil(previous), "name", strOrNil(name), "reportedName", adapter.ReportedName)
	c.pub.name(ctx, updated)
	c.pub.discovery(ctx, updated, c.registry.Sources())
	if updated.Role == dt241m.RoleTransmitter {
		c.pub.refreshSources(ctx)
	}
	return updated, nil
}

func (c *Controller) persist(ctx context.Context, a registry.Adapter) {
	if err := c.store.Save(context.WithoutCancel(ctx), a); err != nil {
		c.log.Error("persist_failed", "mac", a.MAC, "error", err)
	}
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
