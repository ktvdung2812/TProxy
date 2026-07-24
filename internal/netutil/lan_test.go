package netutil

import "testing"

func TestLANIPv4AddressesReturnsSortedUnique(t *testing.T) {
	ips := LANIPv4Addresses()
	if len(ips) == 0 {
		t.Skip("no LAN IPv4 addresses on this host")
	}
	seen := make(map[string]struct{}, len(ips))
	for i, ip := range ips {
		if i > 0 && ips[i-1] > ip {
			t.Fatalf("addresses not sorted: %v", ips)
		}
		if _, ok := seen[ip]; ok {
			t.Fatalf("duplicate address %q in %v", ip, ips)
		}
		seen[ip] = struct{}{}
	}
}
