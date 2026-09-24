package registry

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
)

// MaxNameLength bounds the user-editable name; it is also advertised to Home Assistant.
const MaxNameLength = 64

// Adapter is one DT241M unit. MAC is its identity; IP is only where it was last reached.
//
// Name is a pointer because "no user name" (fall back to the hardware name) is a real
// state distinct from any string. Channel is a pointer because 0 is a valid channel and
// rows persisted before a device ever answered have none. The remaining optional strings
// use "" for absent; empty is never a meaningful value for them.
type Adapter struct {
	MAC          string
	ID           string
	Role         dt241m.Role
	IP           string
	Name         *string
	ReportedName string
	ProductName  string
	Model        string
	Firmware     string
	Channel      *int
	Online       bool
	FirstSeenAt  time.Time
	LastSeenAt   time.Time
}

// DisplayName is the user's name when set, otherwise what the hardware reports.
func (a Adapter) DisplayName() string {
	switch {
	case a.Name != nil:
		return *a.Name
	case a.ReportedName != "":
		return a.ReportedName
	case a.ProductName != "":
		return a.ProductName
	default:
		return a.ID
	}
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
