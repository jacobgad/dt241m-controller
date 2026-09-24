// Package registry is the in-memory inventory of adapters keyed by MAC address.
package registry

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mac"
)

// MaxNameLength bounds the user-editable name; it is also advertised to Home Assistant.
const MaxNameLength = 64

// Adapter is one DT241M unit. MAC is its identity; IP is only where it was last reached.
type Adapter struct {
	MAC          string
	ID           string
	Role         dt241m.Role
	IP           string
	Name         *string
	ReportedName *string
	ProductName  *string
	Model        *string
	Firmware     *string
	Channel      *int
	Online       bool
	FirstSeenAt  time.Time
	LastSeenAt   *time.Time
}

// DisplayName is the user's name when set, otherwise what the hardware reports.
func (a Adapter) DisplayName() string {
	switch {
	case a.Name != nil:
		return *a.Name
	case a.ReportedName != nil:
		return *a.ReportedName
	case a.ProductName != nil:
		return *a.ProductName
	default:
		return a.ID
	}
}

// Observation describes what changed when a device answered a probe.
type Observation struct {
	Adapter         Adapter
	Created         bool
	CameOnline      bool
	IPChanged       bool
	PreviousIP      string
	ChannelChanged  bool
	MetadataChanged bool
	RoleChanged     bool
	Displaced       *Adapter
}

// Counts feeds the controller's Known/Online sensors.
type Counts struct {
	Known  int
	Online int
}

var controlChars = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// ValidateName trims and bounds a name from Home Assistant. A blank name is a
// request to clear it, reported as (nil, nil).
func ValidateName(raw string) (*string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if len([]rune(trimmed)) > MaxNameLength {
		return nil, fmt.Errorf("name longer than %d characters", MaxNameLength)
	}
	if controlChars.MatchString(trimmed) {
		return nil, fmt.Errorf("name contains control characters")
	}
	return &trimmed, nil
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
			LastSeenAt:   &now,
		}
		r.adapters[macAddr] = a
		return Observation{Adapter: *a, Created: true, CameOnline: true, ChannelChanged: true, MetadataChanged: true, Displaced: displaced}, true
	}

	obs := Observation{
		CameOnline:     !existing.Online,
		IPChanged:      existing.IP != ip,
		ChannelChanged: existing.Channel == nil || *existing.Channel != channel,
		RoleChanged:    existing.Role != role,
		Displaced:      displaced,
	}
	if obs.IPChanged {
		obs.PreviousIP = existing.IP
	}
	obs.MetadataChanged = obs.RoleChanged ||
		!strEq(existing.ReportedName, info.DevName) ||
		!strEq(existing.ProductName, info.ProductName) ||
		!strEq(existing.Model, info.Model) ||
		!strEq(existing.Firmware, info.Version)

	existing.IP = ip
	existing.Role = role
	existing.ReportedName = info.DevName
	existing.ProductName = info.ProductName
	existing.Model = info.Model
	existing.Firmware = info.Version
	existing.Channel = &channel
	existing.Online = true
	existing.LastSeenAt = &now
	obs.Adapter = *existing
	return obs, true
}

func strEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
