package controller

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/registry"
)

const publishTimeout = 5 * time.Second

// publisher is the Home Assistant presenter. It decides what an observation means for
// the broker, turns registry state into retained messages, and remembers what it last
// sent so unchanged values are not repeated.
//
// Every entry point holds mu for its whole duration. Retained topics keep only the last
// message, so two publications of the same topic must reach the broker in the order the
// registry changed; a full sweep (everything) must therefore never interleave with a
// per-adapter update taken from a newer snapshot, and vice versa.
type publisher struct {
	conn     mqtt.Connection
	registry *registry.Registry
	origin   mqtt.Origin
	log      *slog.Logger

	mu          sync.Mutex
	lastCounts  *registry.Counts
	lastSources registry.SourceTable
}

// observation publishes whatever an observation changed: discovery for new or re-described
// adapters, availability on transitions, state when values moved, and the receivers'
// Source lists whenever a transmitter was involved.
func (p *publisher) observation(ctx context.Context, obs registry.Observation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a := obs.Adapter
	if obs.Displaced != nil {
		p.availability(ctx, *obs.Displaced)
	}
	if obs.Created || obs.MetadataChanged {
		p.discovery(ctx, a)
		p.name(ctx, a)
	}
	if obs.CameOnline {
		p.availability(ctx, a)
	}
	if obs.Created || obs.StateChanged || obs.RoleChanged {
		p.state(ctx, a)
	}
	if obs.CameOnline || obs.Displaced != nil {
		p.counts(ctx)
	}
	if a.Role == dt241m.RoleTransmitter || obs.RoleChanged {
		p.refreshSources(ctx)
	}
}

// renamed republishes what a user-set name touches: the name state, the device name in
// every discovery config, and the receivers' Source lists if a transmitter was renamed.
func (p *publisher) renamed(ctx context.Context, a registry.Adapter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.name(ctx, a)
	p.discovery(ctx, a)
	if a.Role == dt241m.RoleTransmitter {
		p.refreshSources(ctx)
	}
}

// offline publishes an adapter that stopped answering.
func (p *publisher) offline(ctx context.Context, a registry.Adapter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.availability(ctx, a)
	p.counts(ctx)
}

// rejectedName re-sends the current name after a rejected rename so Home Assistant's text box snaps back.
func (p *publisher) rejectedName(ctx context.Context, a registry.Adapter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.name(ctx, a)
}

// everything re-sends the complete picture; used on connect and on Home Assistant's birth.
func (p *publisher) everything(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publish(ctx, mqtt.ControllerAvailability, mqtt.PayloadOnline)
	for _, m := range mqtt.ControllerMessages(p.origin) {
		p.publish(ctx, m.Topic, m.JSON())
	}
	table := p.registry.Sources()
	p.lastSources = table
	for _, a := range p.registry.All() {
		p.discoveryWith(ctx, a, table)
		p.availability(ctx, a)
		p.name(ctx, a)
		p.stateWith(ctx, a, table)
	}
	counts := p.registry.Counts()
	p.lastCounts = &counts
	p.publishCounts(ctx, counts)
}

func (p *publisher) controllerOffline(ctx context.Context) {
	p.publish(ctx, mqtt.ControllerAvailability, mqtt.PayloadOffline)
}

func (p *publisher) discovery(ctx context.Context, a registry.Adapter) {
	p.discoveryWith(ctx, a, p.registry.Sources())
}

func (p *publisher) discoveryWith(ctx context.Context, a registry.Adapter, table registry.SourceTable) {
	for _, topic := range mqtt.StaleTopics(a) {
		p.publish(ctx, topic, "")
	}
	for _, m := range mqtt.AdapterMessages(a, table.Options(), p.origin) {
		p.publish(ctx, m.Topic, m.JSON())
	}
}

func (p *publisher) availability(ctx context.Context, a registry.Adapter) {
	payload := mqtt.PayloadOffline
	if a.Online {
		payload = mqtt.PayloadOnline
	}
	p.publish(ctx, mqtt.ForDevice(a.MAC).Availability, payload)
}

func (p *publisher) name(ctx context.Context, a registry.Adapter) {
	p.publish(ctx, mqtt.ForDevice(a.MAC).NameState, a.DisplayName())
}

func (p *publisher) state(ctx context.Context, a registry.Adapter) {
	p.stateWith(ctx, a, p.registry.Sources())
}

func (p *publisher) stateWith(ctx context.Context, a registry.Adapter, table registry.SourceTable) {
	t := mqtt.ForDevice(a.MAC)
	if a.Channel != nil {
		p.publish(ctx, t.ChannelState, strconv.Itoa(*a.Channel))
	}
	if a.IP != "" {
		p.publish(ctx, t.IPState, a.IP)
	}
	p.publish(ctx, t.RoleState, string(a.Role))
	if a.Role == dt241m.RoleReceiver {
		p.publish(ctx, t.SourceState, table.ForChannel(a.Channel))
	}
}

// counts publishes the Known/Online sensors when they changed since last time.
func (p *publisher) counts(ctx context.Context) {
	counts := p.registry.Counts()
	unchanged := p.lastCounts != nil && *p.lastCounts == counts
	p.lastCounts = &counts
	if !unchanged {
		p.publishCounts(ctx, counts)
	}
}

// scanFinished publishes counts after a sweep, which may have changed nothing.
func (p *publisher) scanFinished(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts(ctx)
}

func (p *publisher) publishCounts(ctx context.Context, counts registry.Counts) {
	p.publish(ctx, mqtt.ControllerKnownState, strconv.Itoa(counts.Known))
	p.publish(ctx, mqtt.ControllerOnlineState, strconv.Itoa(counts.Online))
}

// refreshSources republishes every receiver's Source select and state when the
// transmitter catalogue (names or channels) differs from what was last published.
func (p *publisher) refreshSources(ctx context.Context) {
	table := p.registry.Sources()
	if table.Equal(p.lastSources) {
		return
	}
	p.lastSources = table
	for ch, macs := range table.Collisions() {
		p.log.Warn("transmitter_channel_collision", "channel", ch, "transmitters", strings.Join(macs, ","))
	}
	for _, rx := range p.registry.All() {
		if rx.Role != dt241m.RoleReceiver {
			continue
		}
		m := mqtt.ReceiverSource(rx, table.Options(), p.origin)
		p.publish(ctx, m.Topic, m.JSON())
		p.publish(ctx, mqtt.ForDevice(rx.MAC).SourceState, table.ForChannel(rx.Channel))
	}
}

func (p *publisher) publish(ctx context.Context, topic, payload string) {
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()
	err := p.conn.Publish(pubCtx, topic, payload, true)
	switch {
	case err == nil:
	case errors.Is(err, mqtt.ErrNotConnected):
		p.log.Debug("mqtt_publish_deferred", "topic", topic)
	default:
		p.log.Warn("mqtt_publish_failed", "topic", topic, "error", err)
	}
}
