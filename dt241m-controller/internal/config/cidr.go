package config

import (
	"fmt"
	"net/netip"
)

// MinScanPrefix caps a single range at 65,534 probes.
const MinScanPrefix = 16

var privateBlocks = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
}

// ParseScanRange accepts a private IPv4 CIDR no wider than MinScanPrefix and returns it masked.
func ParseScanRange(text string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(text)
	if err != nil || !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("%q is not a valid IPv4 CIDR such as 192.168.1.0/24", text)
	}
	prefix = prefix.Masked()
	if prefix.Bits() < MinScanPrefix {
		return netip.Prefix{}, fmt.Errorf("%q is too large; use a prefix of /%d or longer", text, MinScanPrefix)
	}
	if !isPrivate(prefix) {
		return netip.Prefix{}, fmt.Errorf("%q is not a private (RFC 1918) range; refusing to scan it", text)
	}
	return prefix, nil
}

func isPrivate(prefix netip.Prefix) bool {
	for _, block := range privateBlocks {
		if block.Bits() <= prefix.Bits() && block.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

// Hosts returns the probe targets in prefix. Network and broadcast addresses are skipped
// except for /31 and /32, where every address is a host.
func Hosts(prefix netip.Prefix) []string {
	prefix = prefix.Masked()
	size := 1 << (32 - prefix.Bits())
	first, last := 0, size
	if prefix.Bits() < 31 {
		first, last = 1, size-1
	}
	hosts := make([]string, 0, last-first)
	addr := prefix.Addr()
	for i := 0; i < last; i++ {
		if i >= first {
			hosts = append(hosts, addr.String())
		}
		addr = addr.Next()
	}
	return hosts
}

// ScanHosts returns the de-duplicated probe targets across all configured ranges, in range order.
func (o Options) ScanHosts() []string {
	seen := make(map[string]struct{})
	var out []string
	for _, prefix := range o.ScanRanges {
		for _, host := range Hosts(prefix) {
			if _, dup := seen[host]; dup {
				continue
			}
			seen[host] = struct{}{}
			out = append(out, host)
		}
	}
	return out
}

// ScanRangeTexts renders the configured ranges for logging.
func (o Options) ScanRangeTexts() []string {
	out := make([]string, len(o.ScanRanges))
	for i, p := range o.ScanRanges {
		out[i] = p.String()
	}
	return out
}
