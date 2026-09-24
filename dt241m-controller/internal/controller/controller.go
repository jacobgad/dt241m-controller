// Package controller orchestrates the add-on: discovery and polling of DT241M units,
// the per-receiver write queue with identity verification, and everything that is
// published to Home Assistant over MQTT.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
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

// Deps wires a Controller; zero values for Now and the readback policy pick production defaults.
type Deps struct {
	Client           dt241m.Client
	MQTT             mqtt.Connection
	Store            store.Store
	Options          config.Options
	Log              *slog.Logger
	Origin           mqtt.Origin
	Now              func() time.Time
	ReadbackAttempts int
	ReadbackDelay    time.Duration
}

// Status is the result class of a receiver channel change.
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

// FailureReason explains a StatusFailed outcome or a rejected request.
type FailureReason string

// Failure reasons.
const (
	ReasonDeviceNotLocated FailureReason = "device_not_located"
	ReasonNotAReceiver     FailureReason = "not_a_receiver"
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

// Rejected is returned when a request is refused before anything is sent.
type Rejected struct {
	Reason FailureReason
}

func (r *Rejected) Error() string { return "channel change rejected: " + string(r.Reason) }

// RenameOutcome reports a Name entity update.
type RenameOutcome struct {
	Renamed bool
	MAC     string
	Name    *string
	Reason  string
}

const (
	publishTimeout     = 5 * time.Second
	mqttStartupWait    = 10 * time.Second
	defaultReadbacks   = 3
	defaultReadbackGap = 400 * time.Millisecond
)

// Controller is the add-on's long-lived core. Create it with New and drive it with Start/Stop.
type Controller struct {
	Registry *registry.Registry

	client           dt241m.Client
	mqtt             mqtt.Connection
	store            store.Store
	opts             config.Options
	log              *slog.Logger
	origin           mqtt.Origin
	now              func() time.Time
	readbackAttempts int
	readbackDelay    time.Duration

	queue    *keyedQueue
	inflight sync.WaitGroup

	discoveryMu   sync.Mutex
	discoveryDone chan struct{}
	discoveryRuns atomic.Int64

	countsMu   sync.Mutex
	lastCounts *registry.Counts

	stopping    atomic.Bool
	started     atomic.Bool
	pollStarted atomic.Bool
	baseCtx     context.Context
	cancelBase  context.CancelFunc
	pollDone    chan struct{}
}

// New wires the controller to its MQTT connection; nothing talks to hardware until Start.
func New(deps Deps) *Controller {
	c := &Controller{
		Registry:         registry.New(),
		client:           deps.Client,
		mqtt:             deps.MQTT,
		store:            deps.Store,
		opts:             deps.Options,
		log:              deps.Log,
		origin:           deps.Origin,
		now:              deps.Now,
		readbackAttempts: deps.ReadbackAttempts,
		readbackDelay:    deps.ReadbackDelay,
		queue:            newKeyedQueue(),
		pollDone:         make(chan struct{}),
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.readbackAttempts <= 0 {
		c.readbackAttempts = defaultReadbacks
		c.readbackDelay = defaultReadbackGap
	}
	c.baseCtx, c.cancelBase = context.WithCancel(context.Background())

	c.mqtt.OnMessage(mqtt.NewRouter(mqtt.Actions{
		ChannelCommand: func(m string, channel int) {
			if err := c.EnqueueChannelChange(m, channel); err != nil {
				c.log.Error("operation_failed", "mac", m, "requestedChannel", channel, "error", err.Error())
			}
		},
		NameCommand:         func(m, raw string) { c.background(func() { c.Rename(c.baseCtx, m, raw) }) },
		RescanRequested:     func() { c.background(func() { c.RunDiscovery(c.baseCtx, "manual_rescan") }) },
		HomeAssistantOnline: func() { c.background(func() { c.PublishEverything(c.baseCtx) }) },
	}, c.log))
	c.mqtt.OnConnect(func() { c.onMQTTConnected(c.baseCtx) })
	return c
}

func (c *Controller) background(fn func()) {
	c.inflight.Add(1)
	go func() {
		defer c.inflight.Done()
		fn()
	}()
}

// Start loads the persisted inventory, publishes it as unavailable, re-probes last-known
// addresses, runs the first full scan and begins polling. It returns once the scan is done.
func (c *Controller) Start(ctx context.Context) error {
	if !c.started.CompareAndSwap(false, true) {
		return nil
	}
	c.log.Info("controller_started",
		"scanRanges", strings.Join(c.opts.ScanRangeTexts(), ","),
		"pollInterval", c.opts.PollInterval.String(),
		"probeTimeout", c.opts.ProbeTimeout.String(),
		"discoveryConcurrency", c.opts.DiscoveryConcurrency)

	persisted, err := c.store.LoadAll(ctx)
	if err != nil {
		return fmt.Errorf("load adapters: %w", err)
	}
	c.Registry.Hydrate(persisted)
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

	c.pollStarted.Store(true)
	go c.pollLoop()
	return nil
}

// Stop halts polling, waits for in-flight work, publishes the controller offline and closes MQTT.
func (c *Controller) Stop(ctx context.Context) {
	c.stopping.Store(true)
	c.cancelBase()
	if c.pollStarted.Load() {
		<-c.pollDone
	}
	c.discoveryMu.Lock()
	done := c.discoveryDone
	c.discoveryMu.Unlock()
	if done != nil {
		<-done
	}
	c.queue.wait()
	c.inflight.Wait()
	if c.mqtt.Connected() {
		if err := c.mqtt.Publish(ctx, mqtt.ControllerAvailability, mqtt.PayloadOffline, true); err != nil {
			c.log.Warn("operation_failed", "operation", "publish_offline", "error", err.Error())
		}
	}
	if err := c.mqtt.Close(ctx); err != nil {
		c.log.Warn("operation_failed", "operation", "mqtt_close", "error", err.Error())
	}
	c.log.Info("controller_stopped")
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
	ticker := time.NewTicker(c.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.baseCtx.Done():
			return
		case <-ticker.C:
			c.PollKnownDevices(c.baseCtx)
		}
	}
}

func (c *Controller) onMQTTConnected(ctx context.Context) {
	if err := c.mqtt.Subscribe(ctx, mqtt.Subscriptions); err != nil {
		c.log.Error("operation_failed", "operation", "subscribe", "error", err.Error())
	}
	c.PublishEverything(ctx)
}

// PublishEverything re-sends availability, discovery configs and last known state for
// the controller and every adapter. It never contacts the hardware.
func (c *Controller) PublishEverything(ctx context.Context) {
	c.publish(ctx, mqtt.ControllerAvailability, mqtt.PayloadOnline)
	for _, m := range mqtt.ControllerMessages(c.origin) {
		c.publish(ctx, m.Topic, m.JSON())
	}
	for _, a := range c.Registry.All() {
		c.publishAdapterDiscovery(ctx, a)
		c.publishAdapterAvailability(ctx, a)
		c.publishAdapterName(ctx, a)
		c.publishAdapterState(ctx, a)
	}
	c.publishCounts(ctx, true)
}

// RunDiscovery scans every configured range. If a scan is already running the call
// waits for it rather than starting a second one.
func (c *Controller) RunDiscovery(ctx context.Context, reason string) {
	c.discoveryMu.Lock()
	if c.discoveryDone != nil {
		done := c.discoveryDone
		c.discoveryMu.Unlock()
		c.log.Debug("discovery_already_running", "reason", reason)
		<-done
		return
	}
	if c.stopping.Load() {
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
	c.publishCounts(ctx, false)
	c.log.Info("discovery_completed", "reason", reason, "found", found.Load(), "known", c.Registry.Counts().Known, "duration", c.now().Sub(started).String())
}

// ProbeKnownAddresses re-checks every persisted adapter at its last-known IP.
func (c *Controller) ProbeKnownAddresses(ctx context.Context) {
	var candidates []registry.Adapter
	for _, a := range c.Registry.All() {
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
	if c.stopping.Load() {
		return
	}
	adapters := c.Registry.All()
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
			return
		case observeOtherDevice:
			needsRediscovery.Store(true)
			return
		case observeUnreachable:
			c.markOffline(ctx, a.MAC)
			if a.Online {
				needsRediscovery.Store(true)
			}
		}
	})
	if needsRediscovery.Load() && !c.stopping.Load() {
		c.RunDiscovery(ctx, "device_disappeared")
	}
}

func (c *Controller) forEachAdapter(adapters []registry.Adapter, fn func(registry.Adapter)) {
	sem := make(chan struct{}, c.opts.DiscoveryConcurrency)
	var wg sync.WaitGroup
	for _, a := range adapters {
		wg.Add(1)
		sem <- struct{}{}
		go func(a registry.Adapter) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(a)
		}(a)
	}
	wg.Wait()
}

// EnqueueChannelChange validates a request and claims its place in the receiver's
// queue before returning, so calls made in order are executed in order.
func (c *Controller) EnqueueChannelChange(macAddr string, channel int) error {
	_, err := c.enqueueChannelChange(macAddr, channel)
	return err
}

// RequestChannelChange is EnqueueChannelChange followed by waiting for the outcome.
func (c *Controller) RequestChannelChange(ctx context.Context, macAddr string, channel int) (Outcome, error) {
	result, err := c.enqueueChannelChange(macAddr, channel)
	if err != nil {
		return Outcome{}, err
	}
	select {
	case outcome := <-result:
		return outcome, nil
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	}
}

func (c *Controller) enqueueChannelChange(macAddr string, channel int) (<-chan Outcome, error) {
	if c.stopping.Load() {
		return nil, &Rejected{Reason: ReasonShuttingDown}
	}
	if !dt241m.ValidChannel(channel) {
		return nil, &Rejected{Reason: ReasonInvalidChannel}
	}
	adapter, ok := c.Registry.Lookup(mac.Normalize(macAddr))
	if !ok {
		c.log.Warn("channel_command_unknown_device", "mac", macAddr)
		return nil, &Rejected{Reason: ReasonUnknownDevice}
	}
	if adapter.Role != dt241m.RoleReceiver {
		c.log.Warn("channel_command_refused_role", "mac", adapter.MAC, "role", adapter.Role, "requestedChannel", channel)
		return nil, &Rejected{Reason: ReasonNotAReceiver}
	}
	c.log.Info("channel_change_requested", "mac", adapter.MAC, "ip", adapter.IP, "requestedChannel", channel)
	result := make(chan Outcome, 1)
	c.queue.enqueue(adapter.MAC, func() {
		result <- c.executeChannelChange(c.baseCtx, adapter.MAC, channel)
	})
	return result, nil
}

func (c *Controller) executeChannelChange(ctx context.Context, macAddr string, requested int) Outcome {
	ip, ok := c.resolveVerifiedIP(ctx, macAddr)
	if !ok {
		c.log.Error("operation_failed", "mac", macAddr, "requestedChannel", requested, "reason", ReasonDeviceNotLocated)
		return Outcome{Status: StatusFailed, MAC: macAddr, Requested: requested, Reason: ReasonDeviceNotLocated}
	}
	adapter, _ := c.Registry.Lookup(macAddr)
	if adapter.Role != dt241m.RoleReceiver {
		c.log.Warn("channel_command_refused_role", "mac", macAddr, "role", adapter.Role, "requestedChannel", requested)
		return Outcome{Status: StatusFailed, MAC: macAddr, IP: ip, Requested: requested, Reason: ReasonNotAReceiver, Reported: adapter.Channel}
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
		c.log.Warn("channel_change_ambiguous", "mac", macAddr, "ip", ip, "requestedChannel", requested, "error", err.Error())
	default:
		write = WriteRejected
		c.log.Error("operation_failed", "mac", macAddr, "ip", ip, "requestedChannel", requested, "error", err.Error())
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
	for attempt := 1; attempt <= c.readbackAttempts; attempt++ {
		if attempt > 1 && c.readbackDelay > 0 {
			select {
			case <-time.After(c.readbackDelay):
			case <-ctx.Done():
				return reported
			}
		}
		readCtx, cancel := context.WithTimeout(ctx, c.opts.ProbeTimeout)
		info, err := c.client.GetDeviceInfo(readCtx, ip)
		cancel()
		if err != nil {
			c.log.Warn("channel_readback_unavailable", "mac", macAddr, "ip", ip, "attempt", attempt, "error", err.Error())
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
	adapter, ok := c.Registry.Lookup(macAddr)
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
	located, ok := c.Registry.Lookup(macAddr)
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
	offline, changed := c.Registry.MarkOffline(macAddr)
	if !changed {
		return
	}
	c.log.Warn("adapter_offline", "mac", offline.MAC, "ip", offline.IP, "role", offline.Role)
	c.publishAdapterAvailability(ctx, offline)
	c.publishCounts(ctx, false)
	c.persist(ctx, offline)
}

func (c *Controller) applyObservation(ctx context.Context, ip string, info *dt241m.DeviceInfo) {
	obs, ok := c.Registry.RecordObservation(ip, info, c.now())
	if !ok {
		c.log.Warn("adapter_invalid_mac", "ip", ip, "reportedMac", info.LanMAC)
		return
	}
	a := obs.Adapter
	if obs.Displaced != nil {
		c.log.Warn("adapter_ip_taken_over", "mac", obs.Displaced.MAC, "ip", ip, "byMac", a.MAC)
		c.publishAdapterAvailability(ctx, *obs.Displaced)
	}
	if obs.Created {
		c.log.Info("adapter_discovered", "mac", a.MAC, "ip", ip, "role", a.Role, "reportedName", strOrNil(a.ReportedName), "channel", intOrNil(a.Channel))
		c.persist(ctx, a)
		c.publishAdapterDiscovery(ctx, a)
		c.publishAdapterAvailability(ctx, a)
		c.publishAdapterName(ctx, a)
		c.publishAdapterState(ctx, a)
		c.publishCounts(ctx, false)
		return
	}
	if obs.IPChanged {
		c.log.Info("adapter_ip_changed", "mac", a.MAC, "previousIp", obs.PreviousIP, "ip", ip, "role", a.Role)
	}
	if obs.MetadataChanged {
		if obs.RoleChanged {
			c.publish(ctx, mqtt.StaleChannelTopic(a), "")
		}
		c.publishAdapterDiscovery(ctx, a)
		c.publishAdapterName(ctx, a)
	}
	if obs.CameOnline {
		c.log.Info("adapter_online", "mac", a.MAC, "ip", ip, "role", a.Role)
		c.publishAdapterAvailability(ctx, a)
	}
	if obs.ChannelChanged {
		c.log.Info("adapter_channel_observed", "mac", a.MAC, "ip", ip, "role", a.Role, "reportedChannel", intOrNil(a.Channel))
	}
	if obs.ChannelChanged || obs.IPChanged || obs.CameOnline || obs.RoleChanged {
		c.publishAdapterState(ctx, a)
	}
	if obs.CameOnline || obs.Displaced != nil {
		c.publishCounts(ctx, false)
	}
	if obs.IPChanged || obs.ChannelChanged || obs.MetadataChanged || obs.CameOnline {
		c.persist(ctx, a)
	}
}

// Rename stores a user-chosen name for an adapter and republishes its discovery. The
// hardware is never contacted; reportedName is left untouched.
func (c *Controller) Rename(ctx context.Context, macAddr, raw string) RenameOutcome {
	adapter, ok := c.Registry.Lookup(mac.Normalize(macAddr))
	if !ok {
		c.log.Warn("rename_unknown_device", "mac", macAddr)
		return RenameOutcome{MAC: macAddr, Reason: string(ReasonUnknownDevice)}
	}
	name, err := registry.ValidateName(raw)
	if err != nil {
		c.log.Warn("rename_rejected", "mac", adapter.MAC, "reason", err.Error())
		c.publishAdapterName(ctx, adapter)
		return RenameOutcome{MAC: adapter.MAC, Reason: err.Error()}
	}
	previous := adapter.Name
	updated, _ := c.Registry.SetName(adapter.MAC, name)
	if err := c.store.SetName(ctx, adapter.MAC, name); err != nil {
		c.log.Error("persist_failed", "mac", adapter.MAC, "error", err.Error())
	}
	c.log.Info("adapter_renamed", "mac", adapter.MAC, "previousName", strOrNil(previous), "name", strOrNil(name), "reportedName", strOrNil(adapter.ReportedName))
	c.publishAdapterName(ctx, updated)
	c.publishAdapterDiscovery(ctx, updated)
	return RenameOutcome{Renamed: true, MAC: adapter.MAC, Name: name}
}

func (c *Controller) persist(ctx context.Context, a registry.Adapter) {
	if err := c.store.Save(context.WithoutCancel(ctx), a); err != nil {
		c.log.Error("persist_failed", "mac", a.MAC, "error", err.Error())
	}
}

func (c *Controller) publish(ctx context.Context, topic, payload string) {
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()
	if err := c.mqtt.Publish(pubCtx, topic, payload, true); err != nil {
		c.log.Debug("mqtt_publish_deferred", "topic", topic, "error", err.Error())
	}
}

func (c *Controller) publishAdapterDiscovery(ctx context.Context, a registry.Adapter) {
	for _, m := range mqtt.AdapterMessages(a, c.origin) {
		c.publish(ctx, m.Topic, m.JSON())
	}
}

func (c *Controller) publishAdapterAvailability(ctx context.Context, a registry.Adapter) {
	payload := mqtt.PayloadOffline
	if a.Online {
		payload = mqtt.PayloadOnline
	}
	c.publish(ctx, mqtt.ForDevice(a.MAC).Availability, payload)
}

func (c *Controller) publishAdapterName(ctx context.Context, a registry.Adapter) {
	c.publish(ctx, mqtt.ForDevice(a.MAC).NameState, a.DisplayName())
}

func (c *Controller) publishAdapterState(ctx context.Context, a registry.Adapter) {
	t := mqtt.ForDevice(a.MAC)
	if a.Channel != nil {
		c.publish(ctx, t.ChannelState, strconv.Itoa(*a.Channel))
	}
	if a.IP != "" {
		c.publish(ctx, t.IPState, a.IP)
	}
	c.publish(ctx, t.RoleState, string(a.Role))
}

func (c *Controller) publishCounts(ctx context.Context, force bool) {
	counts := c.Registry.Counts()
	c.countsMu.Lock()
	unchanged := c.lastCounts != nil && *c.lastCounts == counts
	c.lastCounts = &counts
	c.countsMu.Unlock()
	if unchanged && !force {
		return
	}
	c.publish(ctx, mqtt.ControllerKnownState, strconv.Itoa(counts.Known))
	c.publish(ctx, mqtt.ControllerOnlineState, strconv.Itoa(counts.Online))
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
