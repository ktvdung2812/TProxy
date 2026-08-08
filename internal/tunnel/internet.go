package tunnel

import (
	"net"
	"sync"
	"time"
)

// Auto-recovery is gated on this check, so a false negative means the tunnel
// stays down until someone intervenes. One hard-coded IP is not enough: 1.1.1.1
// is blocked outright on some ISPs and corporate networks. Probe several
// well-known anycast resolvers plus the hosts our tunnels actually depend on,
// and treat any single success as "online".
var internetProbeTargets = []string{
	"1.1.1.1:443",
	"8.8.8.8:443",
	"9.9.9.9:443",
	"api.trycloudflare.com:443",
	"controlplane.tailscale.com:443",
}

const internetProbeTimeout = 3 * time.Second

func CheckInternet() bool {
	return reachableAny(internetProbeTargets, internetProbeTimeout)
}

// reachableAny dials every target concurrently and returns as soon as one
// connects, so a blocked address costs no extra wall-clock time.
func reachableAny(targets []string, timeout time.Duration) bool {
	if len(targets) == 0 {
		return false
	}
	results := make(chan bool, len(targets))
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func(address string) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", address, timeout)
			if err != nil {
				results <- false
				return
			}
			_ = conn.Close()
			results <- true
		}(target)
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	for ok := range results {
		if ok {
			return true
		}
	}
	return false
}
