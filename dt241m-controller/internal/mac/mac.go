// Package mac normalises DT241M MAC addresses, the sole identity used across the add-on.
package mac

import (
	"regexp"
	"strings"
)

var hex12 = regexp.MustCompile(`^[0-9a-f]{12}$`)
var nonHex = regexp.MustCompile(`[^0-9a-f]`)

// Normalize accepts any common separator or none and returns the canonical
// lower-case colon form, or "" when raw is not a MAC address.
func Normalize(raw string) string {
	compact := nonHex.ReplaceAllString(strings.ToLower(strings.TrimSpace(raw)), "")
	if !hex12.MatchString(compact) {
		return ""
	}
	parts := make([]string, 0, 6)
	for i := 0; i < 12; i += 2 {
		parts = append(parts, compact[i:i+2])
	}
	return strings.Join(parts, ":")
}

// Compact strips the separators for use in topics and identifiers.
func Compact(normalized string) string {
	return strings.ReplaceAll(normalized, ":", "")
}

// AdapterID is the stable Home Assistant object-id prefix for a MAC.
func AdapterID(normalized string) string {
	return "dt241m_" + Compact(normalized)
}
