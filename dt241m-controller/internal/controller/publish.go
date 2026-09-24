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

// publisher is the Home Assistant presenter: it turns registry state into retained
// MQTT messages and remembers what it last sent so unchanged values are not re-sent.
type publisher struct {
	conn     mqtt.Connection
	registry *registry.Registry
	origin   mqtt.Origin
	log      *slog.Logger

	mu          sync.Mutex
	lastCounts  *registry.Counts
	lastSources registry.SourceTable
}

func (p *publisher) everything(ctx context.Context) {
	p.publish(ctx, mqtt.ControllerAvailability, mqtt.PayloadOnline)
	for _, m := range mqtt.ControllerMessages(p.origin) {
		p.publish(ctx, m.Topic, m.JSON())
	}
	table := p.registry.Sources()
	p.mu.Lock()
	p.lastSources = table
	p.mu.Unlock()
	for _, a := range p.registry.All() {
		p.adapter(ctx, a, table)
	}
	p.counts(ctx, true)
}

func (p *publisher) controllerOffline(ctx context.Context) {
	p.publish(ctx, mqtt.ControllerAvailability, mqtt.PayloadOffline)
}

// adapter sends the complete picture of one adapter: discovery, availability, name, state.
func (p *publisher) adapter(ctx context.Context, a registry.Adapter, table registry.SourceTable) {
	p.discovery(ctx, a, table)
	p.availability(ctx, a)
	p.name(ctx, a)
	p.state(ctx, a, table)
}

func (p *publisher) discovery(ctx context.Context, a registry.Adapter, table registry.SourceTable) {
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

func (p *publisher) state(ctx context.Context, a registry.Adapter, table registry.SourceTable) {
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

func (p *publisher) counts(ctx context.Context, force bool) {
	counts := p.registry.Counts()
	p.mu.Lock()
	unchanged := p.lastCounts != nil && *p.lastCounts == counts
	p.lastCounts = &counts
	p.mu.Unlock()
	if unchanged && !force {
		return
	}
	p.publish(ctx, mqtt.ControllerKnownState, strconv.Itoa(counts.Known))
	p.publish(ctx, mqtt.ControllerOnlineState, strconv.Itoa(counts.Online))
}

// refreshSources republishes every receiver's Source select and state when the
// transmitter catalogue (names or channels) differs from what was last published.
func (p *publisher) refreshSources(ctx context.Context) {
	table := p.registry.Sources()
	p.mu.Lock()
	changed := !table.Equal(p.lastSources)
	p.lastSources = table
	p.mu.Unlock()
	if !changed {
		return
	}
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
