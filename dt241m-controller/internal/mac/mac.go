package mac

import (
	"regexp"
	"strings"
)

var hex12 = regexp.MustCompile(`^[0-9a-f]{12}$`)
var nonHex = regexp.MustCompile(`[^0-9a-f]`)

// Normalize returns the lower-case colon-separated form of raw, or "" if raw is not a MAC address.
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

func Compact(normalized string) string {
	return strings.ReplaceAll(normalized, ":", "")
}

func FromCompact(compact string) string {
	return Normalize(compact)
}

func AdapterID(normalized string) string {
	return "dt241m_" + Compact(normalized)
}
