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
// # Known consequence: TCP_USER_TIMEOUT is not set on Linux
//
// Accept returns a WRAPPING net.Conn, because that Close is the only hook that
// can release a slot. grpc-go's transport calls
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
package netlimit

import (
	"net"
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

// hostKey is the HOST part of a peer address, never host:port. Every TCP
// connection has a distinct source port, so keying on the full address would
// make the per-IP cap a no-op that still passes a naive test. Same rule and same
// fallback as api.clientIP (internal/api/ratelimit.go:66-72), so relay has one
// notion of "peer" rather than two.
func hostKey(a net.Addr) string {
	if a == nil {
		return ""
	}
	s := a.String()
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		return s
	}
	return host
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
// The decrement happens AFTER the underlying Close returns: end the generation
// before releasing the resource, so a slot is never handed out while its
// predecessor's file descriptor is still open.
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
