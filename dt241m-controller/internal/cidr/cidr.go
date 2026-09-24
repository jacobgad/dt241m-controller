package cidr

import (
	"fmt"
	"net/netip"
)

const MinScanPrefix = 16

var privateBlocks = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
}

// ParseScanRange validates an IPv4 CIDR for use as a discovery range.
func ParseScanRange(text string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(text)
	if err != nil || !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("%q is not a valid IPv4 CIDR such as 192.168.1.0/24", text)
	}
	prefix = prefix.Masked()
	if prefix.Bits() < MinScanPrefix {
		return netip.Prefix{}, fmt.Errorf("%q is too large; use a prefix of /%d or longer", text, MinScanPrefix)
	}
	if !IsPrivate(prefix) {
		return netip.Prefix{}, fmt.Errorf("%q is not a private (RFC 1918) range; refusing to scan it", text)
	}
	return prefix, nil
}

func IsPrivate(prefix netip.Prefix) bool {
	for _, block := range privateBlocks {
		if block.Bits() <= prefix.Bits() && block.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

// Hosts lists the usable host addresses of prefix, skipping network and broadcast for prefixes shorter than /31.
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

// ExpandAll returns the de-duplicated union of hosts across prefixes, preserving order.
func ExpandAll(prefixes []netip.Prefix) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, prefix := range prefixes {
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

func Texts(prefixes []netip.Prefix) []string {
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		out[i] = p.String()
	}
	return out
}
