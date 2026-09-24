package controller

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mac"
	"github.com/jacobgad/dt241m-controller/internal/registry"
)

func normalize(macAddr string) string { return mac.Normalize(macAddr) }

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

	c.scan(ctx, reason)

	c.discoveryMu.Lock()
	c.discoveryDone = nil
	c.discoveryMu.Unlock()
	close(done)
}

func (c *Controller) scan(ctx context.Context, reason string) {
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
			c.observe(ctx, hit.IP, hit.Info)
		},
	})
	c.pub.scanFinished(ctx)
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
	forEach(ctx, candidates, c.opts.DiscoveryConcurrency, func(a registry.Adapter) {
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
	forEach(ctx, adapters, c.opts.DiscoveryConcurrency, func(a registry.Adapter) {
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

type observeOutcome string

const (
	observeVerified    observeOutcome = "verified"
	observeOtherDevice observeOutcome = "other_device"
	observeUnreachable observeOutcome = "unreachable"
)

// observeAt queries ip and records whatever answers. The outcome says whether it was the
// device we expected, a different one, or nothing at all.
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
		c.observe(ctx, ip, info)
		return observeOtherDevice
	}
	c.observe(ctx, ip, info)
	return observeVerified
}

// observe records a device response, then logs, publishes and persists what changed.
func (c *Controller) observe(ctx context.Context, ip string, info *dt241m.DeviceInfo) {
	obs, ok := c.registry.RecordObservation(ip, info, c.now())
	if !ok {
		c.log.Warn("adapter_invalid_mac", "ip", ip, "reportedMac", info.LanMAC)
		return
	}
	a := obs.Adapter
	if obs.Displaced != nil {
		c.log.Warn("adapter_ip_taken_over", "mac", obs.Displaced.MAC, "ip", ip, "byMac", a.MAC)
	}
	switch {
	case obs.Created:
		c.log.Info("adapter_discovered", "mac", a.MAC, "ip", ip, "role", a.Role, "reportedName", a.ReportedName, "channel", intOrNil(a.Channel))
	case obs.CameOnline:
		c.log.Info("adapter_online", "mac", a.MAC, "ip", ip, "role", a.Role)
	case obs.StateChanged:
		c.log.Info("adapter_state_observed", "mac", a.MAC, "ip", ip, "role", a.Role, "reportedChannel", intOrNil(a.Channel))
	}
	if obs.PreviousIP != "" {
		c.log.Info("adapter_ip_changed", "mac", a.MAC, "previousIp", obs.PreviousIP, "ip", ip, "role", a.Role)
	}
	c.pub.observation(ctx, obs)
	if obs.Changed() {
		c.persist(ctx, a)
	}
}

func (c *Controller) markOffline(ctx context.Context, macAddr string) {
	offline, changed := c.registry.MarkOffline(macAddr)
	if !changed {
		return
	}
	c.log.Warn("adapter_offline", "mac", offline.MAC, "ip", offline.IP, "role", offline.Role)
	c.pub.offline(ctx, offline)
	c.persist(ctx, offline)
}

func (c *Controller) persist(ctx context.Context, a registry.Adapter) {
	if err := c.store.Save(context.WithoutCancel(ctx), a); err != nil {
		c.log.Error("persist_failed", "mac", a.MAC, "error", err)
	}
}
