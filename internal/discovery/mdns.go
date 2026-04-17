package discovery

import (
	"context"
	"fmt"

	"github.com/grandcat/zeroconf"
)

// Browse scans the local network for a relay coordinator advertised as
// _relay._tcp.local and returns the first "host:port" found.
// Returns an error if ctx expires before any service is found.
func Browse(ctx context.Context) (string, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return "", fmt.Errorf("mdns: create resolver: %w", err)
	}

	entries := make(chan *zeroconf.ServiceEntry)
	if err := resolver.Browse(ctx, "_relay._tcp", "local.", entries); err != nil {
		return "", fmt.Errorf("mdns: browse: %w", err)
	}

	for {
		select {
		case entry := <-entries:
			if entry == nil {
				continue
			}
			// IPv6-only hosts are not supported; a coordinator must advertise an IPv4 address.
			if len(entry.AddrIPv4) == 0 {
				continue
			}
			return fmt.Sprintf("%s:%d", entry.AddrIPv4[0], entry.Port), nil
		case <-ctx.Done():
			return "", fmt.Errorf("mdns: no relay coordinator found on local network (use --coordinator to specify address)")
		}
	}
}
