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

const MaxNameLength = 64

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

type Counts struct {
	Known  int
	Online int
}

var controlChars = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// ValidateName trims raw; an empty result means "clear the name" and returns (nil, nil).
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

type Registry struct {
	mu       sync.RWMutex
	adapters map[string]*Adapter
}

func New() *Registry {
	return &Registry{adapters: make(map[string]*Adapter)}
}

func (r *Registry) Hydrate(adapters []Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range adapters {
		copy := a
		copy.Online = false
		r.adapters[a.MAC] = &copy
	}
}

func (r *Registry) Get(macAddr string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[macAddr]
	if !ok {
		return Adapter{}, false
	}
	return *a, true
}

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
