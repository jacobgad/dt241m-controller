// Package registry is the in-memory inventory of adapters keyed by MAC address.
package registry

import (
	"sort"
	"sync"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mac"
)

// Observation describes what changed when a device answered a probe.
//
// StateChanged covers the values Home Assistant shows as state (IP, channel);
// MetadataChanged covers what goes into the device's discovery config (names,
// model, firmware, role). RoleChanged is broken out because a role flip also
// moves entities between platforms.
type Observation struct {
	Adapter         Adapter
	Created         bool
	CameOnline      bool
	StateChanged    bool
	MetadataChanged bool
	RoleChanged     bool
	PreviousIP      string
	Displaced       *Adapter
}

// Changed reports whether anything worth persisting or publishing happened.
func (o Observation) Changed() bool {
	return o.Created || o.CameOnline || o.StateChanged || o.MetadataChanged
}

// Counts feeds the controller's Known/Online sensors.
type Counts struct {
	Known  int
	Online int
}

// Registry is safe for concurrent use; every accessor returns copies.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]*Adapter
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{adapters: make(map[string]*Adapter)}
}

// Hydrate loads persisted adapters, all offline until a probe proves otherwise.
func (r *Registry) Hydrate(adapters []Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range adapters {
		loaded := a
		loaded.Online = false
		r.adapters[a.MAC] = &loaded
	}
}

// Lookup returns the adapter with the given normalised MAC.
func (r *Registry) Lookup(macAddr string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[macAddr]
	if !ok {
		return Adapter{}, false
	}
	return *a, true
}

// All returns every adapter ordered by MAC.
func (r *Registry) All() []Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Adapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC < out[j].MAC })
	return out
}

// Counts tallies known and online adapters.
func (r *Registry) Counts() Counts {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c := Counts{Known: len(r.adapters)}
	for _, a := range r.adapters {
		if a.Online {
			c.Online++
		}
	}
	return c
}

// SetName replaces the user name (nil clears it) and returns the updated adapter.
func (r *Registry) SetName(macAddr string, name *string) (Adapter, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.adapters[macAddr]
	if !ok {
		return Adapter{}, false
	}
	a.Name = name
	return *a, true
}

// MarkOffline flips an adapter offline; ok is false if it was already offline or unknown.
func (r *Registry) MarkOffline(macAddr string) (Adapter, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.adapters[macAddr]
	if !ok || !a.Online {
		return Adapter{}, false
	}
	a.Online = false
	return *a, true
}

// RecordObservation applies a device response seen at ip. Any other adapter that was
// online at the same ip is displaced offline, since one address cannot host two devices.
func (r *Registry) RecordObservation(ip string, info *dt241m.DeviceInfo, now time.Time) (Observation, bool) {
	macAddr := mac.Normalize(info.LanMAC)
	if macAddr == "" {
		return Observation{}, false
	}
	role := dt241m.Classify(info)
	channel := info.ChannelID

	r.mu.Lock()
	defer r.mu.Unlock()

	var displaced *Adapter
	for _, other := range r.adapters {
		if other.MAC != macAddr && other.IP == ip && other.Online {
			other.Online = false
			d := *other
			displaced = &d
			break
		}
	}

	existing, ok := r.adapters[macAddr]
	if !ok {
		a := &Adapter{
			MAC:          macAddr,
			ID:           mac.AdapterID(macAddr),
			Role:         role,
			IP:           ip,
			ReportedName: info.DevName,
			ProductName:  info.ProductName,
			Model:        info.Model,
			Firmware:     info.Version,
			Channel:      &channel,
			Online:       true,
			FirstSeenAt:  now,
			LastSeenAt:   now,
		}
		r.adapters[macAddr] = a
		return Observation{Adapter: *a, Created: true, CameOnline: true, StateChanged: true, MetadataChanged: true, Displaced: displaced}, true
	}

	obs := Observation{
		CameOnline:   !existing.Online,
		StateChanged: existing.IP != ip || existing.Channel == nil || *existing.Channel != channel,
		RoleChanged:  existing.Role != role,
		Displaced:    displaced,
	}
	if existing.IP != ip {
		obs.PreviousIP = existing.IP
	}
	obs.MetadataChanged = obs.RoleChanged ||
		existing.ReportedName != info.DevName ||
		existing.ProductName != info.ProductName ||
		existing.Model != info.Model ||
		existing.Firmware != info.Version

	existing.IP = ip
	existing.Role = role
	existing.ReportedName = info.DevName
	existing.ProductName = info.ProductName
	existing.Model = info.Model
	existing.Firmware = info.Version
	existing.Channel = &channel
	existing.Online = true
	existing.LastSeenAt = now
	obs.Adapter = *existing
	return obs, true
}
