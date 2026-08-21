// Package netlimit bounds how many inbound connections a net.Listener will hand
// to its consumer, in total and per source IP address.
//
// It exists because a bound stated PER CONNECTION is only a bound if connections
// are bounded. relay's agent gRPC port carries several per-connection controls
// (worker.ingestLogLimiter above all) whose security claim is scoped to one
// connection, and nothing bounded connections. See
// docs/superpowers/plans/2026-08-20-grpc-admission-bounds.md.
//
// # Refusal is a close, NEVER an error
//
// Accept refuses an over-limit peer by accepting it, closing it, and looping to
// the next peer. It must never return an error for an over-limit peer.
// grpc.Server.Serve (grpc@v1.80.0/server.go:919-952) treats an Accept error that
// is not Temporary() as FATAL: it returns and its deferred block closes the
// listener. An admission control expressed as an error would take down the
// server it exists to protect. Expressing it as a Temporary() error is also
// wrong - Serve retries those with a 5ms-to-1s backoff, which would rate-limit
// every honest peer queued behind the abusive one.
//
// A real error from the underlying listener still propagates unchanged, or the
// accept loop would spin on a dead socket.
//
// # Known consequences of wrapping the conn: there are TWO
//
// Accept returns a WRAPPING net.Conn, because that Close is the only hook that
// can release a slot. Two things type-assert their way past an interface, so
// both are lost. Neither is lost when both caps are disabled, because Accept
// then returns the underlying conn unwrapped.
//
// FIRST, TCP_USER_TIMEOUT is not set on Linux. grpc-go's transport calls
// internal/syscall.SetTCPUserTimeout(rawConn, kp.Timeout) on the conn it was
// handed (internal/transport/http2_server.go:236-240); that function
// type-asserts conn.(*net.TCPConn) and silently returns nil when the assertion
// fails (internal/syscall/syscall_linux.go:71-76). No interface can satisfy a
// concrete-type assertion, so wrapping loses that socket option on Linux.
//
// The loss is bounded and deliberate: grpc-go's application-layer liveness probe
// is unaffected, because http2Server.keepalive decides from t.lastRead rather
// than from whether a write succeeded, so relay's Time=30s/Timeout=10s still
// tears a dead peer down at 40s. Restoring TCP_USER_TIMEOUT means a build-tagged
// file duplicating a grpc-go internal; that is its own slice.
//
// SECOND, channelz socket metrics go empty. channelz.GetSocketOption asserts
// socket.(syscall.Conn), which this wrapper does not forward, so the per-socket
// options it reports are absent. This is diagnostics-only and relay registers no
// channelz service, so it costs nothing today. Forwarding SyscallConn would
// restore it - and would NOT restore TCP_USER_TIMEOUT, which needs the concrete
// *net.TCPConn and so cannot be recovered through any interface.
package netlimit

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
)

// Config bounds a Listener. A non-positive value DISABLES that cap; it does not
// mean "zero connections allowed". This matches RELAY_GRPC_MAX_CONNS and
// RELAY_GRPC_MAX_CONNS_PER_IP, where 0 is documented as "no bound".
type Config struct {
	MaxTotal int
	MaxPerIP int
}

// Stats is a snapshot of refusal counters. Counts only - never addresses. The
// consumer reports these as a periodic summary, and a summary that could carry
// caller-supplied bytes would be a new attacker-driven log site inside the very
// control that bounds attacker-driven log volume.
type Stats struct {
	RefusedTotal uint64
	RefusedPerIP uint64
}

// Listener is a net.Listener that admits at most Config.MaxTotal live
// connections and at most Config.MaxPerIP from any one source IP.
type Listener struct {
	net.Listener

	cfg Config

	mu    sync.Mutex
	total int
	perIP map[string]int

	refusedTotal atomic.Uint64
	refusedPerIP atomic.Uint64
}

// Wrap returns inner bounded by cfg. Close on the result closes inner, so
// grpc.Server.GracefulStop still shuts the socket down.
func Wrap(inner net.Listener, cfg Config) *Listener {
	return &Listener{Listener: inner, cfg: cfg, perIP: make(map[string]int)}
}

func (l *Listener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		// A net.Listener returning (nil, nil) is out of contract and no stdlib
		// listener does it, but Wrap is exported. SKIP it and accept the next
		// peer: grpc.Server.Serve does not recover its accept goroutine, so a
		// panic on this path kills the process. A nil conn is not a connection,
		// so it holds no slot and has nothing to release.
		//
		// SKIPPING RATHER THAN RETURNING IT IS THE WHOLE POINT. Handing the nil
		// back only MOVES the panic one frame: grpc-go's handleRawConn calls
		// rawConn.SetDeadline with no nil check, from a goroutine it does not
		// recover either (grpc@v1.80.0/server.go:960-974), so the process dies
		// there instead of here. This guard is only a guard because the loop
		// carries on.
		//
		// IT DOES NOT COVER A TYPED NIL, and that is the likelier shape: a
		// (*net.TCPConn)(nil) stored in a net.Conn is not == nil, so it reaches
		// hostKey(c.RemoteAddr()) and panics exactly as before. Distinguishing
		// it needs reflection on every accepted connection, on the hottest path
		// this package has, for a shape no listener in this repo produces. The
		// case is disclosed rather than handled.
		if c == nil {
			continue
		}
		// Both caps disabled: there is no slot to reserve, so the accounting
		// wrapper is pure cost. Returning the conn UNWRAPPED is what lets
		// grpc-go's conn.(*net.TCPConn) assertion succeed and keeps
		// TCP_USER_TIMEOUT on Linux for an operator who caps connections in a
		// proxy instead (see the package doc). cfg is immutable after Wrap, so
		// this branch cannot change under a live connection.
		if l.cfg.MaxTotal <= 0 && l.cfg.MaxPerIP <= 0 {
			return c, nil
		}
		key := hostKey(c.RemoteAddr())
		if l.admit(key) {
			return &conn{Conn: c, lis: l, key: key}, nil
		}
		_ = c.Close()
	}
}

// Stats returns a snapshot of the refusal counters.
func (l *Listener) Stats() Stats {
	return Stats{RefusedTotal: l.refusedTotal.Load(), RefusedPerIP: l.refusedPerIP.Load()}
}

// ipv6AggregationBits is the prefix length IPv6 peers are aggregated to. /64 is
// not a tuning choice: it is the smallest allocation anybody receives, so it is
// the smallest unit whose addresses are not all free to their holder.
//
// WHAT IT RAISES THE BAR TO, RATHER THAN WHAT IT CLOSES. "/64 is the smallest
// delegation" is a correct floor argument for CHOOSING the prefix length and
// says nothing about how much an attacker has to hold. Aggregation moves the
// cost from "one host, one address" to "one host per /64 the attacker holds",
// and no further: at relay's defaults each distinct /64 buys MaxPerIP=64 slots,
// which is 6.25% of MaxTotal=1024, so SIXTEEN distinct /64s fill the fleet cap.
// Providers routinely delegate a /56 (256 /64s) or a /48 (65536), and cheap VPS
// estates hand out a /64 per instance, so a larger delegation escapes this cap
// in exact proportion to its size. The per-source cap is a bar, not a wall; what
// bounds the absolute worst case is MaxTotal, and what makes THAT survivable is
// that a refused peer is closed before any handshake, goroutine or query.
const ipv6AggregationBits = 64

// hostKey is the SOURCE identity a per-source cap counts against. Never
// host:port - every TCP connection has a distinct source port, so keying on the
// full address would make the cap a no-op that still passes a naive test.
//
// IPv6 is aggregated to a /64; IPv4 is keyed on the exact address. The asymmetry
// is the whole point. A /128 key is not a weaker per-source cap for IPv6, it is
// no cap at all: the smallest IPv6 delegation is a /64, every address in it is
// bindable by its holder for free, and each one lands in its own bucket at count
// 1. MaxPerIP then never fires, RefusedPerIP stays at zero, and the operator
// summary reports fleet growth rather than the single host responsible. IPv4 has
// no equivalent - addresses are scarce and already shared through NAT - so
// aggregating it would collapse unrelated operators into one bucket instead.
//
// THIS DELIBERATELY DIFFERS FROM api.clientIP (internal/api/ratelimit.go:66-72),
// which keys on the exact address for both families, and the difference is not
// an inconsistency to be tidied away. A login rate limiter and a connection cap
// face different adversaries: the limiter meters a cost that is already bounded
// per request and whose worst case is guessing attempts, while this bounds a
// held resource whose worst case is a fleet-wide denial that persists for as
// long as the attacker cares to hold it. Aggregation also has a cost - one
// prefix's honest agents share one budget - and that cost is worth paying here
// and not there.
//
// A v4-mapped v6 address is unmapped first, so a dual-stack listener cannot give
// one host two buckets. Anything that is not an IP address at all (a Unix
// socket, a test fake) falls back to the host string, which is a stable key even
// though it is not an address. A net.Addr that parses but carries no host at all
// (net.TCPAddr{IP: nil}) falls out as the empty string, the same key a nil
// net.Addr gets; that is unreachable from a real listener and is listed here so
// the fallback enumeration is complete rather than nearly complete.
//
// TWO DISCLOSED IMPRECISIONS, both availability-only and neither fixed:
//
//   - THE IPv6 ZONE IS DISCARDED. netip.Prefix drops it (PrefixFrom calls
//     withoutZone), so fe80::1%eth0, fe80::1%eth1 and fe80::dead:beef:1:2%eth9
//     all key to fe80::/64 - and fe80::/64 is the ENTIRE link-local space, so
//     the "smallest allocation anybody receives" reasoning above does not hold
//     for it. A dual-homed server whose agents reach it over link-local on two
//     separate LANs charges every one of them to a single 64-slot budget. Not
//     an expected relay topology (agents dial a routable coordinator address),
//     and keying link-local with its zone would make the key interface-specific
//     on one side of a connection only, so this is disclosed rather than fixed.
//   - Aggregation is per /64 and an attacker holding a larger delegation gets
//     one bucket per /64 in it. See ipv6AggregationBits.
func hostKey(a net.Addr) string {
	if a == nil {
		return ""
	}
	s := a.String()
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		host = s
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	p, err := addr.Prefix(ipv6AggregationBits)
	if err != nil {
		return addr.String()
	}
	return p.String()
}

// admit reserves a slot, or counts a refusal. The total is checked first, so a
// connection over both caps is counted against RefusedTotal only.
func (l *Listener) admit(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cfg.MaxTotal > 0 && l.total >= l.cfg.MaxTotal {
		l.refusedTotal.Add(1)
		return false
	}
	if l.cfg.MaxPerIP > 0 && l.perIP[key] >= l.cfg.MaxPerIP {
		l.refusedPerIP.Add(1)
		return false
	}
	l.total++
	l.perIP[key]++
	return true
}

// conn is the accounting wrapper. Close is the ONLY release hook, and grpc-go
// never unwraps: the value Accept returns is stored as http2Server.conn and
// every close path goes through it.
//
// ONE grpc-go PATH BYPASSES IT, by design: handleRawConn skips c.Close() when
// the handshake returns credentials.ErrConnDispatched, because the credential
// implementation has taken ownership of the conn (grpc@v1.80.0/server.go:
// 1024-1026). That path is unreachable today - relay sets no grpc.Creds at all -
// and ordinary TLS would be safe anyway, since tls.Conn.Close closes what it
// wrapped. It matters only if a DISPATCHING handshaker is ever added here, and
// it matters a lot: a slot leaked that way is never released, so the cap
// ratchets down to a permanent lockout.
type conn struct {
	net.Conn
	lis  *Listener
	key  string
	once sync.Once
}

// Close closes the underlying conn and releases the slot exactly once.
//
// The once is load-bearing, not defensive. grpc-go double-closes on its most
// common failure path: a peer that opens TCP and hangs up before the HTTP/2
// preface makes NewServerTransport's deferred t.Close(err) call conn.Close
// (http2_server.go:303-307, :1288) AND newHTTP2Transport call c.Close
// (server.go:1027-1033) on the same conn. Without the once, that over-releases
// and the counter drifts until the cap stops firing.
//
// The decrement happens AFTER the underlying Close returns, and that ordering is
// deliberate but is NOT an instance of CLAUDE.md's "end the generation before
// releasing the resource". That rule is about a generation counter guarding
// whether an async continuation is still current; there is no generation here
// and no staleness guard, and this counter is a capacity semaphore, for which
// releasing last is simply the fail-closed order: a slot is never handed out
// while its predecessor's file descriptor is still open. Fewer live connections
// than the cap says is safe; more is not.
//
// The invariant this type does satisfy is identity-checked teardown: the once
// plus the captured c.key mean a Close releases exactly the slot this conn
// reserved, exactly once, and can never decrement another key's count.
func (c *conn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.lis.release(c.key) })
	return err
}

func (l *Listener) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total--
	if n := l.perIP[key]; n <= 1 {
		delete(l.perIP, key)
	} else {
		l.perIP[key] = n - 1
	}
}
