package controller

import (
	"context"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mac"
)

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
	adapter, ok := c.registry.Lookup(normalize(macAddr))
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
		result <- c.changeChannel(c.lifetime, adapter.MAC, channel)
	})
	return result, nil
}

// ChangeSource tunes a receiver to the transmitter currently carrying the given Source label.
func (c *Controller) ChangeSource(macAddr, label string) (<-chan Outcome, error) {
	adapter, ok := c.registry.Lookup(normalize(macAddr))
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

func (c *Controller) changeChannel(ctx context.Context, macAddr string, requested int) Outcome {
	ip, ok := c.verifiedIP(ctx, macAddr)
	if !ok {
		c.log.Error("operation_failed", "mac", macAddr, "requestedChannel", requested, "reason", ReasonDeviceNotLocated)
		return Outcome{Status: StatusFailed, MAC: macAddr, Requested: requested, Reason: ReasonDeviceNotLocated}
	}
	adapter, _ := c.registry.Lookup(macAddr)
	if !writableRole(adapter.Role) {
		c.log.Warn("channel_command_refused_role", "mac", macAddr, "role", adapter.Role, "requestedChannel", requested)
		return Outcome{Status: StatusFailed, MAC: macAddr, IP: ip, Requested: requested, Reason: ReasonUnknownRole, Reported: adapter.Channel}
	}

	write := c.write(ctx, macAddr, ip, requested)
	reported := c.readBack(ctx, macAddr, ip, requested)

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

func (c *Controller) write(ctx context.Context, macAddr, ip string, channel int) WriteStatus {
	writeCtx, cancel := context.WithTimeout(ctx, c.opts.ProbeTimeout)
	defer cancel()
	err := c.client.SetChannel(writeCtx, ip, channel)
	switch {
	case err == nil:
		c.log.Info("channel_change_acknowledged", "mac", macAddr, "ip", ip, "requestedChannel", channel)
		return WriteAccepted
	case dt241m.Ambiguous(err):
		c.log.Warn("channel_change_ambiguous", "mac", macAddr, "ip", ip, "requestedChannel", channel, "error", err)
		return WriteAmbiguous
	default:
		c.log.Error("operation_failed", "mac", macAddr, "ip", ip, "requestedChannel", channel, "error", err)
		return WriteRejected
	}
}

// readBack polls the device for the requested channel within the readback policy and
// returns the last value it reported, or nil if it never answered.
func (c *Controller) readBack(ctx context.Context, macAddr, ip string, requested int) *int {
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
			c.observe(ctx, ip, info)
			return nil
		}
		c.observe(ctx, ip, info)
		channel := info.ChannelID
		reported = &channel
		if channel == requested {
			return reported
		}
	}
	return reported
}

// verifiedIP returns an address that has just been proven to belong to macAddr,
// rediscovering the device if its last-known address is silent or answers as someone else.
func (c *Controller) verifiedIP(ctx context.Context, macAddr string) (string, bool) {
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
