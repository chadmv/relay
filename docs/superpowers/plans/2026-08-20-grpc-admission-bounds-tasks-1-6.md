# gRPC admission bounds - Tasks 1-6

> **Part 2 of 3.** Read `2026-08-20-grpc-admission-bounds.md` first (goal, refutations, file structure). Tasks 7-12 are in `2026-08-20-grpc-admission-bounds-tasks-7-12.md`.

---

## Task 1: `internal/netlimit` refuses an over-limit source IP without erroring

**Files:**
- Create: `internal/netlimit/listener.go`
- Create: `internal/netlimit/listener_test.go`

- [ ] **Step 1: Write the failing test.** Create `internal/netlimit/listener_test.go`. The fakes at the top are reused by Tasks 2 and 3.

```go
package netlimit

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errDrained is what the fake listener returns once its queue is exhausted. It
// stands in for "no more peers are connecting"; a real listener would block, and
// a blocking fake could hang the suite.
var errDrained = errors.New("fake listener: drained")

// fakeConn is the minimum net.Conn the Listener touches: RemoteAddr and Close.
// The embedded nil net.Conn is deliberate - if a future change makes the
// Listener call anything else, this panics loudly instead of silently working.
type fakeConn struct {
	net.Conn
	remote net.Addr
	closes atomic.Int32
}

func (c *fakeConn) RemoteAddr() net.Addr { return c.remote }
func (c *fakeConn) Close() error         { c.closes.Add(1); return nil }

func newFakeConn(hostPort string) *fakeConn {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		panic("newFakeConn: " + err.Error())
	}
	port := 0
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}
	return &fakeConn{remote: &net.TCPAddr{IP: net.ParseIP(host), Port: port}}
}

// fakeListener hands out queued conns in order, then returns err (default
// errDrained). It never blocks, so no test can hang inside Accept.
type fakeListener struct {
	conns  []net.Conn
	i      int
	err    error
	closed atomic.Int32
}

func (f *fakeListener) Accept() (net.Conn, error) {
	if f.i >= len(f.conns) {
		if f.err != nil {
			return nil, f.err
		}
		return nil, errDrained
	}
	c := f.conns[f.i]
	f.i++
	return c, nil
}
func (f *fakeListener) Close() error   { f.closed.Add(1); return nil }
func (f *fakeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9090} }

// TestLimitListener_RefusesBeyondPerIPCap.
//
// THE OVER-LIMIT CONNECTION IS THIRD, WITH A GOOD ONE FOURTH, AND THAT ORDERING
// IS THE TEST. Refusal is deliberately invisible at Accept: grpc.Server.Serve
// treats a non-Temporary Accept error as FATAL and closes the listener
// (grpc@v1.80.0/server.go:944-951), so an admission control that returns an
// error takes down the server it protects. The only observable consequences are
// that the refused conn was Closed and that Accept CARRIED ON and returned the
// next admissible peer. A test that stopped at the refusal could not tell
// "refused that one" from "died".
func TestLimitListener_RefusesBeyondPerIPCap(t *testing.T) {
	c1 := newFakeConn("10.0.0.1:1001")
	c2 := newFakeConn("10.0.0.1:1002")
	c3 := newFakeConn("10.0.0.1:1003") // over the cap
	c4 := newFakeConn("10.0.0.2:1004") // a different source, must still be admitted
	l := Wrap(&fakeListener{conns: []net.Conn{c1, c2, c3, c4}}, Config{MaxTotal: 100, MaxPerIP: 2})

	got1, err := l.Accept()
	require.NoError(t, err)
	require.NotNil(t, got1)
	_, err = l.Accept()
	require.NoError(t, err)

	got3, err := l.Accept()
	require.NoError(t, err,
		"Accept must NEVER return an error for an over-limit peer: grpc.Server.Serve treats it as fatal")
	assert.Equal(t, c4.remote.String(), got3.RemoteAddr().String(),
		"the third Accept must skip the over-limit peer and return the next admissible one")
	assert.Equal(t, int32(1), c3.closes.Load(), "the refused conn must be closed, not leaked")
	assert.Equal(t, int32(0), c1.closes.Load(), "an admitted conn must not be closed by the limiter")
	assert.Equal(t, uint64(1), l.Stats().RefusedPerIP)
	assert.Equal(t, uint64(0), l.Stats().RefusedTotal)
}

// TestLimitListener_PerIPCapIsKeyedOnHostNotHostPort is the discriminating test
// for the one keying bug that leaves every other test in this file passing.
// Every TCP connection has a distinct source port, so keying the map on
// RemoteAddr().String() makes the per-IP cap a no-op.
func TestLimitListener_PerIPCapIsKeyedOnHostNotHostPort(t *testing.T) {
	c1 := newFakeConn("10.0.0.1:1001")
	c2 := newFakeConn("10.0.0.1:2002")
	c3 := newFakeConn("10.0.0.1:3003") // same host, third distinct port
	c4 := newFakeConn("10.0.0.9:4004")
	l := Wrap(&fakeListener{conns: []net.Conn{c1, c2, c3, c4}}, Config{MaxTotal: 100, MaxPerIP: 2})

	_, err := l.Accept()
	require.NoError(t, err)
	_, err = l.Accept()
	require.NoError(t, err)

	got3, err := l.Accept()
	require.NoError(t, err)
	assert.Equal(t, c4.remote.String(), got3.RemoteAddr().String(),
		"three connections from ONE host on three ports must not all be admitted under a per-IP cap of 2 - "+
			"the map key must be the host, never RemoteAddr().String()")
	assert.Equal(t, int32(1), c3.closes.Load())
}

// TestLimitListener_AcceptErrorFromUnderlyingListenerPropagates guards against
// over-applying "never return an error". A genuine listener failure must still
// reach Serve, or the accept loop spins on a dead socket forever.
func TestLimitListener_AcceptErrorFromUnderlyingListenerPropagates(t *testing.T) {
	boom := errors.New("listener exploded")
	l := Wrap(&fakeListener{err: boom}, Config{MaxTotal: 10, MaxPerIP: 10})

	_, err := l.Accept()
	assert.ErrorIs(t, err, boom, "a real Accept error must propagate unchanged")
}

// Keep the compiler honest about imports used only by Tasks 2 and 3.
var _ = bytes.MinRead
var _ = fmt.Sprintf
var _ = log.Flags
```

(Delete the three `var _ =` lines once Tasks 2 and 3 have added their tests.)

- [ ] **Step 2: Run test to verify it fails.** Run `go test ./internal/netlimit/ -run TestLimitListener -v`. Expected: FAIL - the package does not exist yet (`no Go files in ...`); after `listener.go` is created empty, `undefined: Wrap`, `undefined: Config`.

- [ ] **Step 3: Write minimal implementation.** Create `internal/netlimit/listener.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes.** Run `go test ./internal/netlimit/ -run TestLimitListener -v`. Expected: PASS, three tests.

- [ ] **Step 5: Commit**

```bash
git add internal/netlimit/listener.go internal/netlimit/listener_test.go
git commit -m "feat(netlimit): per-source-IP connection cap that refuses by closing, never by erroring"
```

---

## Task 2: Closing a connection releases its slot exactly once, and the map does not grow

**Files:** Modify `internal/netlimit/listener_test.go` (append three tests; delete the `var _ =` placeholder lines that are now covered).

`release` and the `sync.Once` already exist - `Accept` could not compile without the `conn` type. **These tests are what make them load-bearing**, and step 2 proves it by mutation rather than deferring the proof.

- [ ] **Step 1: Write the failing test.** Append:

```go
// TestLimitListener_CloseReleasesTheSlot. A leak here converts the limiter from
// a cap into a permanent lockout, which is strictly worse than no cap at all.
func TestLimitListener_CloseReleasesTheSlot(t *testing.T) {
	c1 := newFakeConn("10.0.0.1:1001")
	c2 := newFakeConn("10.0.0.1:1002")
	c3 := newFakeConn("10.0.0.1:1003")
	l := Wrap(&fakeListener{conns: []net.Conn{c1, c2, c3}}, Config{MaxTotal: 100, MaxPerIP: 2})

	got1, err := l.Accept()
	require.NoError(t, err)
	_, err = l.Accept()
	require.NoError(t, err)

	require.NoError(t, got1.Close())

	got3, err := l.Accept()
	require.NoError(t, err)
	require.NotNil(t, got3)
	assert.Equal(t, c3.remote.String(), got3.RemoteAddr().String(),
		"closing an admitted conn must free its slot for the same source IP")
	assert.Equal(t, int32(0), c3.closes.Load(), "the third conn was admitted; nothing should have closed it")
}

// TestLimitListener_DoubleCloseReleasesExactlyOneSlot.
//
// grpc-go double-closes routinely, not exceptionally: a peer that opens TCP and
// hangs up before the HTTP/2 preface is closed by NewServerTransport's deferred
// t.Close AND by newHTTP2Transport's c.Close. Without the sync.Once the counter
// over-releases and the cap silently stops firing.
func TestLimitListener_DoubleCloseReleasesExactlyOneSlot(t *testing.T) {
	c1 := newFakeConn("10.0.0.1:1001")
	c2 := newFakeConn("10.0.0.1:1002")
	c3 := newFakeConn("10.0.0.1:1003")
	c4 := newFakeConn("10.0.0.1:1004")
	l := Wrap(&fakeListener{conns: []net.Conn{c1, c2, c3, c4}}, Config{MaxTotal: 100, MaxPerIP: 2})

	got1, err := l.Accept()
	require.NoError(t, err)
	_, err = l.Accept()
	require.NoError(t, err)

	require.NoError(t, got1.Close())
	require.NoError(t, got1.Close(), "a second Close must be a no-op for accounting, not an error")

	// White-box: exactly one slot came back.
	l.mu.Lock()
	total, perIP := l.total, l.perIP["10.0.0.1"]
	l.mu.Unlock()
	assert.Equal(t, 1, total, "a double Close must release ONE slot, not two")
	assert.Equal(t, 1, perIP, "a double Close must release ONE per-IP slot, not two")

	// And behaviourally: exactly ONE further conn is admitted, and the next is refused.
	got3, err := l.Accept()
	require.NoError(t, err)
	assert.Equal(t, c3.remote.String(), got3.RemoteAddr().String())

	got4, err := l.Accept()
	assert.ErrorIs(t, err, errDrained,
		"c4 must be REFUSED and the fake then drained: had the double Close opened a fourth slot, "+
			"Accept would have returned c4 with a nil error")
	assert.Nil(t, got4)
	assert.Equal(t, int32(1), c4.closes.Load(), "the refused c4 must have been closed")
}

// TestLimitListener_ReleasedIPIsRemovedFromTheMap. Without delete-at-zero the
// limiter is itself unbounded memory growth keyed on attacker-chosen source
// addresses - the same defect one layer down from the one it closes.
func TestLimitListener_ReleasedIPIsRemovedFromTheMap(t *testing.T) {
	const n = 1000
	conns := make([]net.Conn, 0, n)
	for i := 0; i < n; i++ {
		conns = append(conns, newFakeConn(fmt.Sprintf("10.0.%d.%d:1000", i/256%256, i%256)))
	}
	l := Wrap(&fakeListener{conns: conns}, Config{MaxTotal: 0, MaxPerIP: 4})

	for i := 0; i < n; i++ {
		c, err := l.Accept()
		require.NoError(t, err)
		require.NoError(t, c.Close())
	}

	l.mu.Lock()
	size, total := len(l.perIP), l.total
	l.mu.Unlock()
	assert.Equal(t, 0, size, "every per-IP entry must be deleted when its count reaches zero")
	assert.Equal(t, 0, total)
}
```

- [ ] **Step 2: Run test to verify it fails.** Run `go test ./internal/netlimit/ -run 'TestLimitListener_(CloseReleases|DoubleClose|ReleasedIP)' -v`. Expected: **PASS**, because Task 1 had to write `release` to compile. That is not a plan failure - it is why the RED must come from mutation instead. Run all three now, reverting each immediately and recording the failure message:

| Mutation in `internal/netlimit/listener.go` | Must FAIL |
|---|---|
| Comment out the body of `release` | `TestLimitListener_CloseReleasesTheSlot` |
| Replace `c.once.Do(func() { c.lis.release(c.key) })` with a bare `c.lis.release(c.key)` | `TestLimitListener_DoubleCloseReleasesExactlyOneSlot` |
| Replace the `delete(l.perIP, key)` branch with an unconditional `l.perIP[key] = n - 1` | `TestLimitListener_ReleasedIPIsRemovedFromTheMap` |

- [ ] **Step 3: Write minimal implementation.** None. If a mutation above does not produce the stated failure, `release`/`Close` are wrong; fix them until it does.

- [ ] **Step 4: Run the full package.** Run `go test ./internal/netlimit/ -v`. Expected: PASS, six tests.

- [ ] **Step 5: Commit**

```bash
git add internal/netlimit/listener_test.go
git commit -m "test(netlimit): pin slot release, double-close idempotence and per-IP map cleanup"
```

---

## Task 3: The total cap, zero-disables, and no log line on the refusal path

**Files:** Modify `internal/netlimit/listener_test.go` (append four tests).

- [ ] **Step 1: Write the failing test.** Append:

```go
// TestLimitListener_TotalCapRefusesAcrossDistinctIPs. The total cap is the only
// one that yields a fleet-wide number, and ingestLogLimiter's doc comment cites
// that number, so it is load-bearing rather than a nicety.
func TestLimitListener_TotalCapRefusesAcrossDistinctIPs(t *testing.T) {
	c1 := newFakeConn("10.0.0.1:1")
	c2 := newFakeConn("10.0.0.2:1")
	c3 := newFakeConn("10.0.0.3:1")
	c4 := newFakeConn("10.0.0.4:1") // over the TOTAL cap, though its own IP has none
	c5 := newFakeConn("10.0.0.5:1")
	l := Wrap(&fakeListener{conns: []net.Conn{c1, c2, c3, c4, c5}}, Config{MaxTotal: 3, MaxPerIP: 100})

	first, err := l.Accept()
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = l.Accept()
		require.NoError(t, err)
	}

	got, err := l.Accept()
	assert.ErrorIs(t, err, errDrained, "c4 and c5 are both over the total cap, so the fake drains")
	assert.Nil(t, got)
	assert.Equal(t, int32(1), c4.closes.Load())
	assert.Equal(t, int32(1), c5.closes.Load())
	assert.Equal(t, uint64(2), l.Stats().RefusedTotal)
	assert.Equal(t, uint64(0), l.Stats().RefusedPerIP,
		"a conn over BOTH caps is counted against the total only; the total is checked first")

	require.NoError(t, first.Close())
	l.mu.Lock()
	total := l.total
	l.mu.Unlock()
	assert.Equal(t, 2, total, "releasing one slot must re-open the total budget")
}

// TestLimitListener_ZeroDisables. 0 means "no bound", not "no connections". An
// operator who prefers the kernel's ceiling must be able to say so.
func TestLimitListener_ZeroDisables(t *testing.T) {
	const n = 200
	conns := make([]net.Conn, 0, n)
	for i := 0; i < n; i++ {
		conns = append(conns, newFakeConn(fmt.Sprintf("10.0.0.1:%d", 1000+i)))
	}
	l := Wrap(&fakeListener{conns: conns}, Config{MaxTotal: 0, MaxPerIP: 0})

	for i := 0; i < n; i++ {
		c, err := l.Accept()
		require.NoError(t, err, "conn %d must be admitted when both caps are disabled", i)
		require.NotNil(t, c)
	}
	assert.Equal(t, Stats{}, l.Stats(), "nothing may be counted as refused when both caps are off")
}

// TestLimitListener_RefusalWritesNothingToTheLog.
//
// A log.Printf per refusal would be a new, unbounded, ATTACKER-DRIVEN log site
// inside the very control that exists to bound attacker-driven log volume - the
// 2026-08-15 lesson one layer down. Refusals are surfaced by the consumer as a
// periodic summary of counts (cmd/relay-server's refusalReporter); this package
// must stay silent. Asserting this in the reporter's own test would prove
// nothing: adding a line HERE leaves the reporter perfectly correct.
func TestLimitListener_RefusalWritesNothingToTheLog(t *testing.T) {
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })

	const n = 100
	conns := make([]net.Conn, 0, n)
	for i := 0; i < n; i++ {
		conns = append(conns, newFakeConn(fmt.Sprintf("10.0.0.1:%d", 1000+i)))
	}
	l := Wrap(&fakeListener{conns: conns}, Config{MaxTotal: 1000, MaxPerIP: 1})

	for {
		if _, err := l.Accept(); err != nil {
			break
		}
	}
	require.Equal(t, uint64(n-1), l.Stats().RefusedPerIP, "99 of the 100 must have been refused")
	assert.Equal(t, 0, buf.Len(),
		"netlimit must write NOTHING to the log on the refusal path. Got: %q", buf.String())
}

// TestLimitListener_CloseClosesTheUnderlyingListener keeps GracefulStop working:
// Serve's deferred ls.Close() must reach the real socket.
func TestLimitListener_CloseClosesTheUnderlyingListener(t *testing.T) {
	inner := &fakeListener{}
	l := Wrap(inner, Config{MaxTotal: 1, MaxPerIP: 1})
	require.NoError(t, l.Close())
	assert.Equal(t, int32(1), inner.closed.Load())
}
```

**No test in this file may call `t.Parallel()`** - `log.SetOutput` is process-global.

- [ ] **Step 2: Run test to verify it fails.** Run `go test ./internal/netlimit/ -run 'TestLimitListener_(TotalCap|ZeroDisables|RefusalWrites|CloseCloses)' -v`. Expected PASS, then prove load-bearing by mutation, reverting each immediately:

| Mutation | Must FAIL |
|---|---|
| Delete the `l.cfg.MaxTotal > 0 && l.total >= l.cfg.MaxTotal` branch in `admit` | `TestLimitListener_TotalCapRefusesAcrossDistinctIPs` |
| Change both `> 0` guards in `admit` to `>= 0` | `TestLimitListener_ZeroDisables` |
| Add `log.Printf("netlimit: refused %s", key)` before either `return false` in `admit` | `TestLimitListener_RefusalWritesNothingToTheLog` |

- [ ] **Step 3: Write minimal implementation.** None expected. Delete the three `var _ =` placeholder lines from Task 1's test file now that `bytes`, `fmt` and `log` are genuinely used.

- [ ] **Step 4: Run the full package.** Run `go test ./internal/netlimit/ -v`, then `go test -race ./internal/netlimit/`. Expected: PASS, ten tests. (On Windows `-race` needs MSYS2 mingw64 gcc: `CC=/c/msys64/mingw64/bin/gcc.exe`. If unavailable, say so in the task report; Task 12 re-runs it.)

- [ ] **Step 5: Commit**

```bash
git add internal/netlimit/listener_test.go
git commit -m "test(netlimit): total cap, zero-disables, and the no-log-on-refusal guard"
```

---

## Task 4: `MaxConcurrentStreams(1)` and the keepalive options, behind a testable option builder

**Files:**
- Create: `cmd/relay-server/grpc_config.go`
- Create: `cmd/relay-server/grpc_config_test.go`
- Create: `cmd/relay-server/grpc_server_test.go`

Neither test file gets a build tag. `bootstrap_test.go` and `startup_reconcile_test.go` in this package are `//go:build integration`; a tag here would silently remove these from `make test`.

- [ ] **Step 1: Write the failing test.** Create `cmd/relay-server/grpc_server_test.go`:

```go
package main

import (
	"context"
	"net"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// blockingAgentService is a stub AgentServiceServer that holds every stream open
// until its context is cancelled and sends nothing. Sending nothing matters:
// grpc-go resets the ping-strike counter on every outgoing DATA or HEADERS frame
// (http2_server.go:1046, :1114, :1158), and a chatty stub would mask policy
// behaviour. No database is involved, so these tests stay in `make test`.
type blockingAgentService struct {
	relayv1.UnimplementedAgentServiceServer
	entered chan struct{}
}

func (s *blockingAgentService) Connect(stream grpc.BidiStreamingServer[relayv1.AgentMessage, relayv1.CoordinatorMessage]) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-stream.Context().Done()
	return nil
}

// startTestGRPCServer serves the PRODUCTION option list over a fresh loopback
// listener and returns its address plus a channel that receives once per stream
// the handler enters. The listener is the raw one - connection-cap wiring is
// exercised by Task 9's end-to-end test, not here.
func startTestGRPCServer(t *testing.T, b grpcBounds) (string, chan struct{}) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	stub := &blockingAgentService{entered: make(chan struct{}, 8)}
	srv := grpc.NewServer(grpcServerOptions(b)...)
	relayv1.RegisterAgentServiceServer(srv, stub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), stub.entered
}

func dialTestServer(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

// waitReady forces the transport up and waits for the HTTP/2 handshake, which is
// what guarantees the server's SETTINGS frame has been applied client-side.
func waitReady(t *testing.T, cc *grpc.ClientConn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cc.Connect()
	for {
		s := cc.GetState()
		if s == connectivity.Ready {
			return
		}
		require.True(t, cc.WaitForStateChange(ctx, s), "transport never became READY")
	}
}

// TestGRPCServer_SecondStreamOnOneConnectionBlocks.
//
// THE DEADLINE IS THE ASSERTION, NOT A CONVENIENCE. A compliant grpc-go client
// never sees the server's RST_STREAM(REFUSED_STREAM): checkForStreamQuota parks
// on t.streamsQuotaAvailable with ctx.Done() as the only escape
// (http2_client.go:829-836, :885-908). A bare "expect an error" here would hang
// forever.
//
// RED at HEAD: with no SETTINGS_MAX_CONCURRENT_STREAMS advertised
// (http2_server.go:178-183) the client falls back to 100 (defaults.go:34), so
// the second stream opens instantly and err is nil.
func TestGRPCServer_SecondStreamOnOneConnectionBlocks(t *testing.T) {
	addr, entered := startTestGRPCServer(t, grpcBounds{})
	cc := dialTestServer(t, addr)
	waitReady(t, cc)
	client := relayv1.NewAgentServiceClient(cc)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	_, err := client.Connect(ctx1)
	require.NoError(t, err, "the first stream must open")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the server handler never entered for stream 1")
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	start := time.Now()
	_, err = client.Connect(ctx2)
	require.Error(t, err,
		"a SECOND concurrent stream on ONE connection must not open: grpcMaxConcurrentStreams is 1")
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	assert.GreaterOrEqual(t, time.Since(start), 1500*time.Millisecond,
		"it must have BLOCKED on stream quota until the deadline, not failed fast for some other reason")
}
```

Create `cmd/relay-server/grpc_config_test.go`:

```go
package main

import (
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// oneStreamPerConnection is the predicate behind the structural guard below. It
// is a named function so the guard can be exercised against SYNTHETIC service
// descriptors: mutating the real one would mean editing relay.proto and running
// buf generate, which this slice forbids, and a mutation you cannot run is a
// claim rather than a kill.
func oneStreamPerConnection(d grpc.ServiceDesc) error {
	if len(d.Methods) != 0 {
		return fmt.Errorf("%s has %d unary method(s); expected 0", d.ServiceName, len(d.Methods))
	}
	if len(d.Streams) != 1 {
		return fmt.Errorf("%s has %d stream(s); expected 1", d.ServiceName, len(d.Streams))
	}
	return nil
}

// TestAgentServiceHasExactlyOneStreamPerConnection is a tripwire, not a
// behaviour test: it is GREEN at HEAD by design and its failure message is the
// deliverable. grpcMaxConcurrentStreams is 1 because "one stream per connection"
// is a property of the wire contract. If AgentService ever gains a second RPC, a
// compliant client BLOCKS on stream quota rather than erroring - a miserable
// thing to debug in production. This turns that into a red test naming the fix.
func TestAgentServiceHasExactlyOneStreamPerConnection(t *testing.T) {
	require.NoError(t, oneStreamPerConnection(relayv1.AgentService_ServiceDesc),
		"AgentService no longer has exactly one streaming RPC and zero unary methods. RAISE "+
			"grpcMaxConcurrentStreams in cmd/relay-server/grpc_config.go to the new number of concurrent "+
			"streams an agent needs, or a client opening the second one will block until its deadline "+
			"with no error to explain why.")

	// The predicate must reject the shapes it exists to catch, or this guard is a
	// green test that checks nothing.
	assert.Error(t, oneStreamPerConnection(grpc.ServiceDesc{
		ServiceName: "synthetic", Streams: []grpc.StreamDesc{{}, {}}}),
		"two streams must be rejected")
	assert.Error(t, oneStreamPerConnection(grpc.ServiceDesc{
		ServiceName: "synthetic", Methods: []grpc.MethodDesc{{}}, Streams: []grpc.StreamDesc{{}}}),
		"a unary method must be rejected")
}

// TestGRPCKeepaliveParamsKeepsTheLivenessProbe.
//
// MaxConnectionIdle lives in the SAME keepalive.ServerParameters struct as the
// existing Time/Timeout liveness probe. Appending a second
// grpc.KeepaliveParams(...) option compiles, is the obvious way to write this
// diff, and silently discards Time and Timeout because the later option
// overwrites o.keepaliveParams wholesale (grpc@v1.80.0/server.go:330-332). This
// is the test that makes that regression red.
func TestGRPCKeepaliveParamsKeepsTheLivenessProbe(t *testing.T) {
	kp := grpcKeepaliveParams(15 * time.Minute)
	assert.Equal(t, 30*time.Second, kp.Time, "the 30s inactivity ping must survive")
	assert.Equal(t, 10*time.Second, kp.Timeout, "the 10s ping-ack deadline must survive")
	assert.Equal(t, 15*time.Minute, kp.MaxConnectionIdle)
	assert.Zero(t, kp.MaxConnectionAge,
		"MaxConnectionAge is deliberately OUT of this slice: it terminates connections that are doing "+
			"their job and costs a log chunk per forced reconnect. It has its own backlog item.")

	assert.Zero(t, grpcKeepaliveParams(0).MaxConnectionIdle,
		"0 must pass straight through: grpc-go maps a zero MaxConnectionIdle to infinity "+
			"(http2_server.go:219-221), so 'disabled' needs no relay-side branch")
}

// TestGRPCEnforcementPolicyMatchesGRPCsOwnDefault is a CONSTANT LOCKSTEP CHECK
// AND NOTHING MORE, and that is stated rather than implied.
//
// The policy we ship is behaviourally identical to grpc-go's default
// (defaultKeepalivePolicyMinTime = 5m, defaults.go:40; PermitWithoutStream's zero
// value is false), so no test can be RED at HEAD for it. A behavioural test is
// also unavailable: grpc.WithKeepaliveParams CLAMPS the client ping interval to
// internal.KeepaliveMinPingTime = 10s (dialoptions.go:561-565), and that knob is
// in a package relay cannot import - so the fastest honest abusive-pinger test
// would take ~40s and would straddle the exact value a realistic regression
// would use. What this test buys is that LOWERING MinTime - the only change that
// makes the option matter, and a regression - shows up as a red test in the diff
// that does it.
func TestGRPCEnforcementPolicyMatchesGRPCsOwnDefault(t *testing.T) {
	ep := grpcEnforcementPolicy()
	assert.Equal(t, 5*time.Minute, ep.MinTime,
		"5m is not picked, it IS grpc-go's defaultKeepalivePolicyMinTime. Anything smaller LOOSENS a "+
			"control; anything larger refuses pings grpc-go accepts today, for a principal that sends none "+
			"(the agent sets no client keepalive at all - internal/agent/agent.go:196-202).")
	assert.False(t, ep.PermitWithoutStream,
		"PermitWithoutStream=false is not independently testable in reasonable time - distinguishing it "+
			"from true needs a client pinging slower than MinTime with no stream, i.e. over five minutes. "+
			"This assertion pins the decision, not the behaviour.")
}
```

Add `"fmt"` to `grpc_config_test.go`'s imports.

- [ ] **Step 2: Run test to verify it fails.** Run `go test ./cmd/relay-server/ -run 'TestGRPCServer_SecondStream|TestAgentServiceHasExactly|TestGRPCKeepalive|TestGRPCEnforcement' -v`. Expected: FAIL to build - `undefined: grpcBounds`, `undefined: grpcServerOptions`, `undefined: grpcKeepaliveParams`, `undefined: grpcEnforcementPolicy`.

- [ ] **Step 3: Write minimal implementation.** Create `cmd/relay-server/grpc_config.go` (the env parsers, bounds line and reporter arrive in Tasks 6-8; this is the option half):

```go
package main

import (
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// grpcMaxConcurrentStreams is 1 because AgentService has exactly ONE RPC and
// zero unary methods (internal/proto/relayv1/relay_grpc.pb.go:102-115), and an
// agent opens exactly one stream per connection and dials a fresh ClientConn per
// reconnect attempt (internal/agent/agent.go:202-209). "One stream per
// connection" is therefore a property of the wire contract, not a convention.
//
// THERE IS DELIBERATELY NO ENV KNOB. The only legitimate reason for this to move
// is a proto change, and TestAgentServiceHasExactlyOneStreamPerConnection catches
// that with a message naming this constant. An operator knob here could only
// LOOSEN a security control: the value multiplies worker.ingestLogLimiter's
// per-connection budget one-for-one, because that limiter is allocated once per
// Connect call, i.e. once per STREAM (internal/worker/handler.go:172).
//
// Cost to a legitimate agent: zero. Cost to an attacker: the per-connection
// multiplication now needs a connection, which netlimit bounds.
const grpcMaxConcurrentStreams = 1

// grpcBounds is the resolved, already-parsed admission configuration. It is a
// plain struct so tests can construct a server from the PRODUCTION option list
// with a 200ms idle timeout and no env var, no global and no build tag.
type grpcBounds struct {
	maxConns      int           // total live connections; 0 disables
	maxConnsPerIP int           // live connections per source IP; 0 disables
	maxConnIdle   time.Duration // reap a transport with no stream; 0 disables
}

// grpcServerOptions is the complete option list for the agent gRPC server.
// EXACTLY ONE grpc.KeepaliveParams may appear here - see grpcKeepaliveParams.
func grpcServerOptions(b grpcBounds) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams),
		grpc.KeepaliveParams(grpcKeepaliveParams(b.maxConnIdle)),
		grpc.KeepaliveEnforcementPolicy(grpcEnforcementPolicy()),
	}
}

// grpcKeepaliveParams is ONE struct carrying three decisions, and it is a
// separate function because they must not be split across two options: grpc-go
// stores keepaliveParams wholesale (server.go:330-332), so a second
// grpc.KeepaliveParams(...) silently discards Time and Timeout.
//
//   - Time/Timeout are the pre-existing liveness probe. Unchanged.
//   - MaxConnectionIdle reaps a transport that completed the HTTP/2 preface and
//     then never opened a stream. It can NEVER terminate a connection that is
//     doing its job: t.idle is zeroed when the first stream opens and re-stamped
//     only when the last one closes (http2_server.go:582-585, :1299-1306), and a
//     zero t.idle reschedules rather than reaping (:1204-1220). That is exactly
//     why it is here and MaxConnectionAge is not.
//   - A zero idle value is passed straight through: grpc-go maps it to infinity
//     (:219-221), so "disabled" needs no branch here.
//
// Note what MaxConnectionIdle does NOT cover: a peer that completes TCP and then
// says nothing at all never reaches transport construction, so it is bounded
// instead by grpc-go's connectionTimeout, 120s by default (server.go:193).
func grpcKeepaliveParams(idle time.Duration) keepalive.ServerParameters {
	return keepalive.ServerParameters{
		Time:              30 * time.Second, // ping after 30s of transport inactivity
		Timeout:           10 * time.Second, // close the transport if no ack within 10s
		MaxConnectionIdle: idle,
	}
}

// grpcKeepaliveMinTime is grpc-go's OWN defaultKeepalivePolicyMinTime
// (internal/transport/defaults.go:40), restated here on purpose.
//
// This value is not picked; it is the unique non-regressive one. grpc-go already
// enforces a 5m floor whether you set a policy or not (http2_server.go:241-244),
// so anything smaller is a LOOSENING. Anything larger would start refusing pings
// grpc-go accepts today, and no principal sends them: the agent configures no
// client keepalive at all (internal/agent/agent.go:196-202), so
// defaultClientKeepaliveTime is infinity and it sends none, ever.
//
// LOWERING THIS IS THE ONLY WAY TO MAKE IT MATTER, AND IS A REGRESSION. The
// realistic failure mode this line exists to prevent is somebody "adding a
// keepalive policy" with MinTime: 10*time.Second because that is what the
// internet suggests, silently loosening a control by a factor of 30.
const grpcKeepaliveMinTime = 5 * time.Minute

func grpcEnforcementPolicy() keepalive.EnforcementPolicy {
	return keepalive.EnforcementPolicy{
		MinTime:             grpcKeepaliveMinTime,
		PermitWithoutStream: false,
	}
}
```

- [ ] **Step 4: Run test to verify it passes.** Run `go test ./cmd/relay-server/ -run 'TestGRPCServer_SecondStream|TestAgentServiceHasExactly|TestGRPCKeepalive|TestGRPCEnforcement' -v`. Expected: PASS, four tests, in under 5s.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/grpc_config.go cmd/relay-server/grpc_config_test.go cmd/relay-server/grpc_server_test.go
git commit -m "feat(server): one stream per gRPC connection, plus an explicit keepalive enforcement policy"
```

---

## Task 5: `MaxConnectionIdle` reaps a streamless transport and never a busy one

**Files:** Modify `cmd/relay-server/grpc_server_test.go` (append a helper and two tests). No production change - `grpcKeepaliveParams` already carries the field; these tests are what make it load-bearing.

- [ ] **Step 1: Write the failing test.** Append to `cmd/relay-server/grpc_server_test.go`:

```go
// signalListener reports every server-side conn Close on ch. This is a
// SERVER-SIDE signal on purpose: asserting via the client's connectivity state
// would depend on grpc-go's reconnect state machine rather than on whether the
// transport was reaped. ch is buffered because grpc-go double-closes routinely.
type signalListener struct {
	net.Listener
	ch chan struct{}
}

func (l *signalListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &signalConn{Conn: c, ch: l.ch}, nil
}

type signalConn struct {
	net.Conn
	ch chan struct{}
}

func (c *signalConn) Close() error {
	err := c.Conn.Close()
	select {
	case c.ch <- struct{}{}:
	default:
	}
	return err
}

func startTestGRPCServerWithCloseSignal(t *testing.T, b grpcBounds) (string, chan struct{}, chan struct{}) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closed := make(chan struct{}, 16)

	stub := &blockingAgentService{entered: make(chan struct{}, 8)}
	srv := grpc.NewServer(grpcServerOptions(b)...)
	relayv1.RegisterAgentServiceServer(srv, stub)
	go func() { _ = srv.Serve(&signalListener{Listener: raw, ch: closed}) }()
	t.Cleanup(srv.Stop)
	return raw.Addr().String(), stub.entered, closed
}

// TestGRPCServer_IdleConnectionWithNoStreamIsClosed.
//
// RED at HEAD: defaultMaxConnectionIdle is infinity (defaults.go:35), so the
// connection is never reaped and this fails on its 5s bound. Without this
// option, a connection cap is a PARKING PRIMITIVE: an attacker completes the
// HTTP/2 preface, opens no stream, and holds RELAY_GRPC_MAX_CONNS_PER_IP slots
// forever.
func TestGRPCServer_IdleConnectionWithNoStreamIsClosed(t *testing.T) {
	addr, _, closed := startTestGRPCServerWithCloseSignal(t, grpcBounds{maxConnIdle: 200 * time.Millisecond})
	cc := dialTestServer(t, addr)
	waitReady(t, cc) // preface done, transport up, zero streams

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("a transport that completed the HTTP/2 preface and opened no stream was never closed: " +
			"MaxConnectionIdle is not set")
	}
}

// TestGRPCServer_ConnectionHoldingAStreamIsNotIdle proves the section-2.5 reading
// of t.idle and prices MaxConnectionIdle at zero for a legitimate agent, which
// holds ONE silent stream for hours at a time. It is also what catches
// MaxConnectionAge-style semantics slipping in under this name.
func TestGRPCServer_ConnectionHoldingAStreamIsNotIdle(t *testing.T) {
	addr, entered, closed := startTestGRPCServerWithCloseSignal(t, grpcBounds{maxConnIdle: 200 * time.Millisecond})
	cc := dialTestServer(t, addr)
	waitReady(t, cc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := relayv1.NewAgentServiceClient(cc).Connect(ctx)
	require.NoError(t, err)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the server handler never entered")
	}

	// Ten idle windows of complete silence in both directions.
	time.Sleep(2 * time.Second)

	select {
	case <-closed:
		t.Fatal("a connection HOLDING A STREAM was closed. MaxConnectionIdle must never terminate a " +
			"working connection - if this fails, something gave it MaxConnectionAge semantics, which is " +
			"explicitly out of scope for this slice.")
	default:
	}
	require.NoError(t, stream.Send(&relayv1.AgentMessage{}),
		"the stream must still be usable after ten idle windows")
}
```

- [ ] **Step 2: Run test to verify it fails.** Run `go test ./cmd/relay-server/ -run 'TestGRPCServer_Idle|TestGRPCServer_ConnectionHolding' -v`. Expected: `TestGRPCServer_IdleConnectionWithNoStreamIsClosed` PASSES (Task 4 already wired `MaxConnectionIdle`). Prove both are load-bearing now, reverting each immediately:

| Mutation in `grpcKeepaliveParams` | Must FAIL |
|---|---|
| Delete the `MaxConnectionIdle: idle` field | `TestGRPCServer_IdleConnectionWithNoStreamIsClosed` |
| Add `MaxConnectionAge: idle, MaxConnectionAgeGrace: 10 * time.Millisecond` | `TestGRPCServer_ConnectionHoldingAStreamIsNotIdle` (grpc-go adds up to +10% jitter to MaxConnectionAge at http2_server.go:226, which is why the hold is 2s against a 200ms value) |

- [ ] **Step 3: Write minimal implementation.** None expected.

- [ ] **Step 4: Run the package.** Run `go test ./cmd/relay-server/ -v`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/grpc_server_test.go
git commit -m "test(server): idle gRPC transports are reaped, busy ones are not"
```

---

## Task 6: Env parsing for the three knobs

**Files:** Modify `cmd/relay-server/grpc_config.go` and `cmd/relay-server/grpc_config_test.go`.

- [ ] **Step 1: Write the failing test.** Append to `cmd/relay-server/grpc_config_test.go`:

```go
// TestParseConnLimit mirrors TestParseWatchdogDuration's table. Three outcomes,
// not two, which is why the second return is a message and not an ok bool.
//
// ONE DELIBERATE DEVIATION FROM parseWatchdogDuration: there is no `floor`
// outcome. A floor exists to catch units confusion (`24m` for `24h`), and a bare
// connection count has no units to confuse. Any positive value is a legitimate
// operator choice about fleet size or NAT topology.
func TestParseConnLimit(t *testing.T) {
	const def = 1024
	cases := []struct {
		name     string
		raw      string
		want     int
		wantMsg  string
	}{
		{"unset keeps the default and is silent", "", def, ""},
		{"a valid value is used as-is", "64", 64, ""},
		{"1 is accepted without comment", "1", 1, ""},
		{"zero is ACCEPTED and disables the cap, with an informational line", "0", 0, "disabled"},
		{"negative keeps the default and warns", "-5", def, "not a non-negative integer"},
		{"unparseable keeps the default and warns", "lots", def, "not a non-negative integer"},
		{"a float keeps the default and warns", "64.5", def, "not a non-negative integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseConnLimit("RELAY_GRPC_MAX_CONNS", tc.raw, def)
			assert.Equal(t, tc.want, got)
			if tc.wantMsg == "" {
				assert.Empty(t, msg, "a valid value must not produce startup noise")
				return
			}
			require.Contains(t, msg, tc.wantMsg,
				"the message is the only signal an operator gets; it must name the consequence")
			assert.Contains(t, msg, "RELAY_GRPC_MAX_CONNS", "the message must name the variable it is about")
		})
	}
}

// TestParseGRPCConnIdle keeps parseWatchdogDuration's four-outcome shape,
// INCLUDING the floor - unlike the integer knob, this one has a fail-aggressive
// direction (a sub-second value reaps a legitimate agent between its dial and its
// first stream, so it reconnect-loops forever).
func TestParseGRPCConnIdle(t *testing.T) {
	def := 15 * time.Minute
	cases := []struct {
		name    string
		raw     string
		want    time.Duration
		wantMsg string
	}{
		{"unset keeps the default and is silent", "", def, ""},
		{"a valid value is used as-is", "5m", 5 * time.Minute, ""},
		{"zero is ACCEPTED and disables reaping, with an informational line", "0s", 0, "disabled"},
		{"negative keeps the default and warns", "-5m", def, "not a Go duration"},
		{"unparseable keeps the default and warns", "fifteen", def, "not a Go duration"},
		{"below the floor KEEPS the value and warns", "200ms", 200 * time.Millisecond, "below"},
		{"exactly the floor is silent", "1s", time.Second, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseGRPCConnIdle("RELAY_GRPC_MAX_CONN_IDLE", tc.raw, def)
			assert.Equal(t, tc.want, got)
			if tc.wantMsg == "" {
				assert.Empty(t, msg)
				return
			}
			require.Contains(t, msg, tc.wantMsg)
			assert.Contains(t, msg, "RELAY_GRPC_MAX_CONN_IDLE")
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails.** Run `go test ./cmd/relay-server/ -run 'TestParseConnLimit|TestParseGRPCConnIdle' -v`. Expected: FAIL to build - `undefined: parseConnLimit`, `undefined: parseGRPCConnIdle`.

- [ ] **Step 3: Write minimal implementation.** Append to `cmd/relay-server/grpc_config.go` (and add `"fmt"` and `"strconv"` to its imports):

```go
// parseConnLimit resolves one of the two gRPC connection caps into the value
// handed to netlimit.Config, plus a startup message to log, empty when there is
// nothing to say. It follows parseWatchdogDuration's three-outcome shape
// (cmd/relay-server/watchdog_config.go:41), with its own prose:
//
//   - Unset, or a valid non-negative integer: used as-is, silently.
//   - Exactly zero: ACCEPTED, and the cap is disabled. Because disabling an
//     admission control must never be silent, this returns an informational line
//     naming what is now unbounded.
//   - Negative or unparseable: the default is used and the message says so. A
//     silently-ignored typo would leave an operator believing they had tightened
//     a security-relevant knob they had not.
//
// There is deliberately NO floor outcome, unlike parseWatchdogDuration. A floor
// catches units confusion, and a bare connection count has no units; any positive
// value is a legitimate statement about fleet size or NAT topology.
//
// Not a log.Fatalf, following parseTrailingLogWindow and parseWatchdogDuration:
// a bad limit must not stop a server booting when a safe default exists.
func parseConnLimit(name, raw string, def int) (int, string) {
	if raw == "" {
		return def, ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def, fmt.Sprintf("%s=%q is not a non-negative integer; using %d", name, raw, def)
	}
	if n == 0 {
		return 0, fmt.Sprintf(
			"%s=%q: this gRPC connection cap is disabled. Admission on the agent port is bounded only by "+
				"the process file-descriptor limit, and every per-connection control (including "+
				"worker.ingestLogLimiter's log budget) multiplies without a ceiling.", name, raw)
	}
	return n, ""
}

// minGRPCConnIdleDur is the floor for RELAY_GRPC_MAX_CONN_IDLE. A legitimate
// agent's idle window is the gap between grpc.NewClient dialing and
// client.Connect opening its stream (internal/agent/agent.go:202-209), which is
// sub-millisecond on a LAN. The floor is not that number - it is the point below
// which a scheduling stall on a loaded host could plausibly exceed the window,
// GOAWAYing agents before they ever open a stream and leaving them
// reconnect-looping. One second is three orders above the real window and still
// well inside "obviously a mistake" territory.
const minGRPCConnIdleDur = time.Second

// parseGRPCConnIdle resolves RELAY_GRPC_MAX_CONN_IDLE. Same contract as
// parseWatchdogDuration, floor included: this knob DOES have a fail-aggressive
// direction, so a sub-floor value is KEPT and warned about rather than rejected.
func parseGRPCConnIdle(name, raw string, def time.Duration) (time.Duration, string) {
	if raw == "" {
		return def, ""
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return def, fmt.Sprintf("%s=%q is not a Go duration (or is negative); using %s", name, raw, def)
	}
	if d == 0 {
		return 0, fmt.Sprintf(
			"%s=%q: idle gRPC transport reaping is disabled. A peer that completes the HTTP/2 handshake "+
				"and never opens a stream now holds its connection slot forever, which turns the "+
				"connection caps into a parking primitive.", name, raw)
	}
	if d < minGRPCConnIdleDur {
		return d, fmt.Sprintf(
			"%s=%q resolves to %s, below the %s floor. Using it anyway, but a legitimate agent may be "+
				"disconnected between dialing and opening its stream and will reconnect-loop. Check the "+
				"units (%s, not %s?).", name, raw, d, minGRPCConnIdleDur, def, d)
	}
	return d, ""
}
```

- [ ] **Step 4: Run test to verify it passes.** Run `go test ./cmd/relay-server/ -run 'TestParseConnLimit|TestParseGRPCConnIdle' -v`. Expected: PASS, all sub-tests.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/grpc_config.go cmd/relay-server/grpc_config_test.go
git commit -m "feat(server): parse RELAY_GRPC_MAX_CONNS, _PER_IP and _MAX_CONN_IDLE"
```

---

**Continue with `2026-08-20-grpc-admission-bounds-tasks-7-12.md`.**
