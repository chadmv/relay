package netlimit

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
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

// concurrentFakeListener is a thread-safe fake, kept SEPARATE from fakeListener
// so the ordinary tests keep the unsynchronised version whose races would be a
// bug in the test rather than in the code under test. It blocks nowhere: once
// drained it returns errDrained like its sibling.
type concurrentFakeListener struct {
	mu    sync.Mutex
	conns []net.Conn
	i     int
}

func (f *concurrentFakeListener) Accept() (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.i >= len(f.conns) {
		return nil, errDrained
	}
	c := f.conns[f.i]
	f.i++
	return c, nil
}
func (f *concurrentFakeListener) Close() error { return nil }
func (f *concurrentFakeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9090}
}

// TestLimitListener_ConcurrentAcceptAndCloseKeepsTheCountersConsistent.
//
// NOT IN THE PLAN - added because every other test in this file is
// single-goroutine, so `go test -race` over them proves nothing about the one
// piece of shared mutable state this package has (the per-IP map and the two
// counters under l.mu). Production's shape is exactly this one: grpc.Server.Serve
// calls Accept from ONE goroutine, while Close arrives from every connection's
// own transport goroutines, concurrently and repeatedly (grpc-go double-closes
// routinely - see conn.Close).
//
// The assertion is that the accounting is EXACT after the storm, not merely that
// -race is quiet: a lost update under the mutex would leave l.total above zero
// and permanently consume slots, which is the lockout failure mode.
func TestLimitListener_ConcurrentAcceptAndCloseKeepsTheCountersConsistent(t *testing.T) {
	const (
		hosts    = 8
		perHost  = 50
		capPerIP = 4
	)
	conns := make([]net.Conn, 0, hosts*perHost)
	for i := 0; i < hosts*perHost; i++ {
		conns = append(conns, newFakeConn(fmt.Sprintf("10.0.0.%d:%d", i%hosts, 1000+i)))
	}
	l := Wrap(&concurrentFakeListener{conns: conns}, Config{MaxTotal: 16, MaxPerIP: capPerIP})

	var wg sync.WaitGroup
	var admitted atomic.Int64
	for {
		c, err := l.Accept()
		if err != nil {
			break
		}
		admitted.Add(1)
		// Two goroutines racing to Close the SAME conn, which is the double-close
		// grpc-go performs, run concurrently with the next Accept.
		wg.Add(2)
		for i := 0; i < 2; i++ {
			go func() { defer wg.Done(); _ = c.Close() }()
		}
	}
	wg.Wait()

	assert.Positive(t, admitted.Load(), "the storm must actually have admitted something")
	l.mu.Lock()
	total, size := l.total, len(l.perIP)
	l.mu.Unlock()
	assert.Equal(t, 0, total,
		"every admitted conn was closed, so the total must be exactly 0: a non-zero value means slots were "+
			"lost and the cap has become a permanent lockout")
	assert.Equal(t, 0, size, "every per-IP entry must be gone once its count reaches zero")
}

// TestLimitListener_PerIPRefusalConsumesNoPerIPSlot.
//
// A REFUSAL MUST COST THE REFUSED PEER NOTHING BUT THE REFUSAL. If the per-IP
// refusal branch also incremented the map it guards, the first peer to hit its
// cap would be locked out FOREVER - the count could never fall back below the
// cap, because a release only ever happens for a conn that was admitted. That is
// strictly worse than having no cap, and it is also unbounded memory growth
// keyed on attacker-chosen source addresses, which is the very defect
// TestLimitListener_ReleasedIPIsRemovedFromTheMap claims to prevent.
//
// The mirror of this on the TOTAL branch was already pinned, by the trailing
// total assertion in TestLimitListener_TotalCapRefusesAcrossDistinctIPs. This
// arm was not, and `l.perIP[key]++` in the per-IP refusal branch survived the
// entire suite.
//
// MaxTotal is deliberately far above MaxPerIP: the total is checked FIRST, so a
// tight total would shadow the per-IP branch and this test would never reach the
// code it exists to cover.
func TestLimitListener_PerIPRefusalConsumesNoPerIPSlot(t *testing.T) {
	c1 := newFakeConn("10.0.0.1:1001")
	c2 := newFakeConn("10.0.0.1:1002")
	c3 := newFakeConn("10.0.0.1:1003") // over the per-IP cap
	c4 := newFakeConn("10.0.0.1:1004")
	fl := &fakeListener{conns: []net.Conn{c1, c2, c3}}
	l := Wrap(fl, Config{MaxTotal: 100, MaxPerIP: 2})

	got1, err := l.Accept()
	require.NoError(t, err)
	_, err = l.Accept()
	require.NoError(t, err)

	_, err = l.Accept()
	require.ErrorIs(t, err, errDrained, "c3 is over the per-IP cap, so the fake drains behind it")
	require.Equal(t, uint64(1), l.Stats().RefusedPerIP)

	// White-box: the refusal moved no accounting at all.
	l.mu.Lock()
	total, perIP := l.total, l.perIP["10.0.0.1"]
	l.mu.Unlock()
	assert.Equal(t, 2, perIP,
		"a REFUSED conn must not be counted against its source IP: it will never be released, so the "+
			"count could never fall back under the cap and that host would be locked out permanently")
	assert.Equal(t, 2, total, "a per-IP refusal must not consume a slot in the fleet-wide total either")

	// Behavioural: that is what "locked out permanently" means. Releasing an
	// admitted slot must let the same host straight back in.
	require.NoError(t, got1.Close())
	fl.conns = append(fl.conns, c4)
	got4, err := l.Accept()
	require.NoError(t, err)
	require.NotNil(t, got4)
	assert.Equal(t, c4.remote.String(), got4.RemoteAddr().String(),
		"after a refusal AND a release, the same source IP must be admitted again - if it is not, hitting "+
			"the cap once is a permanent lockout for that host")
	assert.Equal(t, int32(0), c4.closes.Load())
}

// TestLimitListener_PerIPRefusalConsumesNoTotalSlot is the other half: a peer
// refused by the PER-IP cap must not eat the FLEET budget, or one noisy host
// ratchets the total cap down to zero and refuses every other source.
//
// MaxTotal is tight here on purpose, but still loose enough that the per-IP
// branch is the one that fires for c3.
func TestLimitListener_PerIPRefusalConsumesNoTotalSlot(t *testing.T) {
	c1 := newFakeConn("10.0.0.1:1001")
	c2 := newFakeConn("10.0.0.1:1002")
	c3 := newFakeConn("10.0.0.1:1003") // refused: per-IP cap, NOT the total
	c4 := newFakeConn("10.0.0.2:1004")
	c5 := newFakeConn("10.0.0.3:1005")
	fl := &fakeListener{conns: []net.Conn{c1, c2, c3}}
	l := Wrap(fl, Config{MaxTotal: 4, MaxPerIP: 2})

	for i := 0; i < 2; i++ {
		_, err := l.Accept()
		require.NoError(t, err)
	}
	_, err := l.Accept()
	require.ErrorIs(t, err, errDrained)
	require.Equal(t, uint64(1), l.Stats().RefusedPerIP, "c3 must be refused by the PER-IP cap, not the total")
	require.Equal(t, uint64(0), l.Stats().RefusedTotal)

	// Two slots were consumed, not three, so two remain under MaxTotal: 4.
	fl.conns = append(fl.conns, c4, c5)
	for _, want := range []*fakeConn{c4, c5} {
		got, err := l.Accept()
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, want.remote.String(), got.RemoteAddr().String(),
			"a per-IP refusal must leave the fleet budget untouched; if it charged the total, this "+
				"unrelated source would be refused and one host could ratchet the total cap to zero")
	}
}

