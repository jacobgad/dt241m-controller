package registry

import (
	"fmt"
	"sort"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
)

// SourceNone is the select state published when no transmitter uses the receiver's channel.
// Home Assistant treats a payload of "none" as "no selection" and never lists it as an option.
const SourceNone = "none"

// Source is a transmitter as offered to receivers: a label unique among transmitters and
// the channel selecting it will tune to.
type Source struct {
	Label   string
	MAC     string
	Channel int
}

// SourceTable is the transmitter catalogue derived from the registry at one instant.
type SourceTable struct {
	sources []Source
}

// Sources builds the table from every known transmitter with a reported channel.
// Duplicate display names are disambiguated with their channel so each label is selectable.
func (r *Registry) Sources() SourceTable {
	var transmitters []Adapter
	for _, a := range r.All() {
		if a.Role == dt241m.RoleTransmitter && a.Channel != nil {
			transmitters = append(transmitters, a)
		}
	}
	nameCount := make(map[string]int, len(transmitters))
	for _, tx := range transmitters {
		nameCount[tx.DisplayName()]++
	}
	sources := make([]Source, 0, len(transmitters))
	for _, tx := range transmitters {
		label := tx.DisplayName()
		if nameCount[label] > 1 {
			label = fmt.Sprintf("%s (ch %d)", label, *tx.Channel)
		}
		sources = append(sources, Source{Label: label, MAC: tx.MAC, Channel: *tx.Channel})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Label < sources[j].Label })
	return SourceTable{sources: sources}
}

// Options lists the selectable labels in display order.
func (t SourceTable) Options() []string {
	out := make([]string, len(t.sources))
	for i, s := range t.sources {
		out[i] = s.Label
	}
	return out
}

// ByLabel resolves a selected option.
func (t SourceTable) ByLabel(label string) (Source, bool) {
	for _, s := range t.sources {
		if s.Label == label {
			return s, true
		}
	}
	return Source{}, false
}

// ForChannel names the transmitter on channel, or SourceNone. When several transmitters
// share a channel the first by label wins; Collisions reports the conflict.
func (t SourceTable) ForChannel(channel *int) string {
	if channel == nil {
		return SourceNone
	}
	for _, s := range t.sources {
		if s.Channel == *channel {
			return s.Label
		}
	}
	return SourceNone
}

// Collisions lists channels claimed by more than one transmitter.
func (t SourceTable) Collisions() map[int][]string {
	byChannel := make(map[int][]string)
	for _, s := range t.sources {
		byChannel[s.Channel] = append(byChannel[s.Channel], s.MAC)
	}
	for ch, macs := range byChannel {
		if len(macs) < 2 {
			delete(byChannel, ch)
		}
	}
	return byChannel
}

// Equal reports whether two tables would produce the same select configuration and states.
func (t SourceTable) Equal(o SourceTable) bool {
	if len(t.sources) != len(o.sources) {
		return false
	}
	for i := range t.sources {
		if t.sources[i] != o.sources[i] {
			return false
		}
	}
	return true
}
