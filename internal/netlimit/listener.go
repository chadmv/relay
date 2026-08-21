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
)

// Config bounds a Listener. A non-positive value DISABLES that cap; it does not
// mean "zero connections allowed". This matches RELAY_GRPC_MAX_CONNS and
// RELAY_GRPC_MAX_CONNS_PER_IP, where 0 is documented as "no bound".
type Config struct {
	MaxTotal int
	MaxPerIP int
}

// RefusalCounts are MONOTONIC totals since process start. They only ever
// increase, which is what makes them safe to compare: a consumer that wants to
// know whether anything happened can compare two snapshots of this half.
//
// Comparable by == deliberately. cmd/relay-server's refusalReporter stores one
// of these and compares, and that must keep compiling.
type RefusalCounts struct {
	RefusedTotal uint64

	// RefusedPerIP UNDER-REPORTS whenever the fleet cap is also saturated:
	// admit checks the total first, so a connection over BOTH caps is counted
	// here as zero and against RefusedTotal only. That is deliberate and is not
	// being changed. What makes it interpretable is Occupancy: when LiveTotal
	// has reached the configured MaxTotal, read this number as a FLOOR rather
	// than as a measurement.
	RefusedPerIP uint64
}

// Occupancy is the CURRENT state of the two caps. Every field is a level, not a
// count: it goes down as well as up.
//
// LEVELS ARE NEVER CONSULTED TO DECIDE WHETHER A REPORTER SPEAKS. Occupancy
// changes on essentially every connection, so a periodic summary that included
// it in its "did anything move" test would emit a line every single interval
// forever - which is the property TestRefusalSummaryLogsOnlyWhenCountersMove
// exists to protect. Levels are carried IN the line when it speaks. Splitting
// them from RefusalCounts is what makes that structural: refusalReporter.last
// is typed RefusalCounts, so comparing a whole Stats does not compile.
type Occupancy struct {
	LiveTotal       uint64
	DistinctSources uint64
	MaxPerSource    uint64
}

// Stats is a snapshot of this listener's counters and levels.
//
// RULE, NOT DESCRIPTION: nothing in this type may ever carry an address, a
// prefix, a hostname, or any other caller-supplied byte. The refusal path is
// reachable by any unauthenticated peer, and the consumer reports these as a
// periodic log summary, so a field carrying caller-supplied bytes would be a new
// attacker-driven log site inside the very control that bounds attacker-driven
// log volume. Counts and levels only, forever - "which IP is it?" is answered
// NO on the record, and TestStats_CarriesNoIdentifiers enforces it by walking
// this type with reflection rather than by trusting this paragraph.
//
// PER REPLICA. These are in-process numbers about ONE listener. A two-server
// deployment splits its connections arbitrarily; an operator must read both
// endpoints and add the counts, and must NOT add the levels - MaxPerSource in
// particular does not sum into anything meaningful.
type Stats struct {
	Counts RefusalCounts
	Levels Occupancy
}

// Listener is a net.Listener that admits at most Config.MaxTotal live
// connections and at most Config.MaxPerIP from any one source IP.
type Listener struct {
	net.Listener

	cfg Config

	// EVERYTHING BELOW mu IS GUARDED BY mu, INCLUDING THE TWO COUNTERS. They
	// were atomic.Uint64 incremented under this same mutex, which made Stats a
	// consistent five-field snapshot - and nothing except a comment held that
	// true. Deciding over-cap under the lock, unlocking, then Add(1) outside
	// left netlimit, cmd/relay-server and internal/api all green, and a poller
	// would then see refused_total climbing while live_total sat BELOW the
	// configured cap: an arrangement the fleet was never in. As plain fields the
	// compiler forbids a read outside the lock and -race catches a write outside
	// it, so the coupling is a type-level fact rather than a paragraph.
	mu           sync.Mutex
	total        int
	perIP        map[string]int
	refusedTotal uint64
	refusedPerIP uint64
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

// Stats returns ONE snapshot of every counter and every level, taken in a
// SINGLE critical section.
//
// THE SINGLE CRITICAL SECTION IS THE CONTRACT, not an implementation detail.
// Three separate lock acquisitions let a caller observe a combination that never
// existed - DistinctSources greater than LiveTotal is directly reachable while
// connections are being admitted - and an operator would then draw a conclusion
// from an arrangement the fleet was never in. Pinned by
// TestStats_IsOneCriticalSection, which is RED under exactly that mutation.
//
// All five fields are read from state guarded by l.mu (see the Listener type),
// so this is one consistent snapshot rather than five individually-correct
// numbers taken at five different moments. TestStats_IsOneCriticalSection pins
// the level-to-level half by invariant. The count-to-level half has NO test that
// can pin it - counts are monotonic and levels move freely, so no single
// snapshot is impossible enough to assert on - which is exactly why the counters
// are plain fields under this mutex rather than atomics: the enforcement is the
// compiler plus -race, and TestStats_ConcurrentRefusalsAndReadsShareTheMutex is
// the concurrent exposure that gives -race something to see.
//
// COST, PRICED AS A LOCK HOLD AND NOT AS A REQUEST. MaxPerSource is an
// O(len(perIP)) walk under l.mu. len(perIP) is bounded by MaxTotal (1024 at the
// defaults) only while the TOTAL cap is enabled; with RELAY_GRPC_MAX_CONNS=0 and
// a live per-source cap, admit still runs and perIP is bounded only by the
// process file-descriptor limit, so the walk is proportional to live
// connections.
//
// THERE ARE TWO CALLERS, NOT ONE. cmd/relay-server's runRefusalReporter calls
// this on a 60s ticker (grpc_config.go), unauthenticated, on EVERY deployment
// whether or not anybody polls the endpoint - it is the caller that always runs.
// The other is the admin-authenticated GET /v1/server/counters handler. Pricing
// the walk against that handler's BearerAuth round trip, as this paragraph used
// to, is wrong twice over: it omits the reporter, and BearerAuth is paid by the
// poller in a different goroutine and has completed before the handler runs, so
// it never overlaps holding l.mu. What the walk actually delays is the ACCEPT
// PATH, whose other holders are admit and release - once per TCP connection, not
// per message.
//
// Measured on a 24-core dev box, ns per Stats() call: ~7us at 1024 entries,
// ~0.6ms at 100k, ~8ms at 1M. At the defaults the hold is negligible against a
// once-per-connection mutex. At a million live sources every accept queues
// behind an ~8ms hold, once a minute for the reporter plus once per admin
// request - and nothing rate-limits the route, since RateLimit is applied to
// POST /v1/auth/register and POST /v1/auth/login only (server.go). That
// configuration is the one README tells an operator to cap in a proxy instead;
// this is what it costs if they do not.
//
// Maintaining the maximum incrementally is NOT cheaper - a decremented maximum
// is not exactly recoverable without a scan, which would move this walk onto
// release, a path much closer to hot than this one.
//
// WHEN BOTH CAPS ARE DISABLED, EVERY LEVEL READS ZERO NO MATTER HOW MANY
// CONNECTIONS ARE LIVE. Accept returns the conn unwrapped in that configuration
// and never calls admit, so nothing is counted. A zero here therefore means "not
// measured", not "nothing there". Pinned by TestLimitListener_ZeroDisables.
func (l *Listener) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	maxPer := 0
	for _, n := range l.perIP {
		if n > maxPer {
			maxPer = n
		}
	}
	return Stats{
		Counts: RefusalCounts{
			RefusedTotal: l.refusedTotal,
			RefusedPerIP: l.refusedPerIP,
		},
		// uint64 AT THE BOUNDARY, and this is load-bearing rather than tidy:
		// the consumer's summary line asserts that every argument it carries is
		// a uint64 (TestRefusalSummaryLogsOnlyWhenCountersMove), which is what
		// keeps caller-supplied bytes out of an attacker-reachable log site. An
		// int occupancy figure turns that shipped test RED.
		//
		// No clamp on the conversion. l.total cannot go negative - release runs
		// exactly once per admitted conn, enforced by conn.once - and if an
		// accounting bug ever made it negative, an absurd number here is a
		// better signal than a zero that hides it.
		Levels: Occupancy{
			LiveTotal:       uint64(l.total),
			DistinctSources: uint64(len(l.perIP)),
			MaxPerSource:    uint64(maxPer),
		},
	}
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
		l.refusedTotal++
		return false
	}
	if l.cfg.MaxPerIP > 0 && l.perIP[key] >= l.cfg.MaxPerIP {
		l.refusedPerIP++
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
