package netlimit

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"reflect"
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
	assert.Equal(t, uint64(1), l.Stats().Counts.RefusedPerIP)
	assert.Equal(t, uint64(0), l.Stats().Counts.RefusedTotal)
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
	assert.Equal(t, uint64(2), l.Stats().Counts.RefusedTotal)
	assert.Equal(t, uint64(0), l.Stats().Counts.RefusedPerIP,
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
	assert.Equal(t, Stats{}, l.Stats(),
		"with both caps off the listener does no accounting at all, so EVERY field stays zero - not just "+
			"the refusal counts. That is what makes a zero level here mean 'not measured' rather than "+
			"'nothing there', which README and Stats both have to state because the payload cannot.")
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
	require.Equal(t, uint64(n-1), l.Stats().Counts.RefusedPerIP, "99 of the 100 must have been refused")
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
	require.Equal(t, uint64(1), l.Stats().Counts.RefusedPerIP)

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
	require.Equal(t, uint64(1), l.Stats().Counts.RefusedPerIP, "c3 must be refused by the PER-IP cap, not the total")
	require.Equal(t, uint64(0), l.Stats().Counts.RefusedTotal)

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

// newFakeConnAddr builds a fakeConn from an already-split host, so an IPv6
// literal needs no bracket bookkeeping in the test body.
func newFakeConnAddr(host string, port int) *fakeConn {
	return &fakeConn{remote: &net.TCPAddr{IP: net.ParseIP(host), Port: port}}
}

// TestLimitListener_PerIPCapAggregatesAnIPv6Prefix.
//
// KEYING IPv6 ON THE EXACT /128 DOES NOT WEAKEN THE PER-IP CAP, IT VOIDS IT. The
// smallest IPv6 allocation anybody gets - residential, cloud, colo - is a /64,
// and every address in it is bindable by its holder at no cost. An attacker
// keyed per /128 therefore presents 1024 distinct "source IPs" from ONE host,
// each landing in its own bucket at count 1, so MaxPerIP never fires at all and
// MaxTotal fills. RefusedPerIP stays at zero throughout, so the operator's
// once-a-minute summary points at "the fleet outgrew the total cap" rather than
// at the one host doing it.
//
// This matters more than it would for a rate limiter, because the per-IP cap is
// the ONLY thing standing between MaxTotal and a fleet-wide denial: a total cap
// with no working per-source cap is a shared bucket any single peer can drain.
func TestLimitListener_PerIPCapAggregatesAnIPv6Prefix(t *testing.T) {
	c1 := newFakeConnAddr("2001:db8:1:2::1", 1001)
	c2 := newFakeConnAddr("2001:db8:1:2::2", 1002)
	c3 := newFakeConnAddr("2001:db8:1:2:dead:beef:0:1", 1003) // same /64, third address
	c4 := newFakeConnAddr("2001:db8:9:9::1", 1004)            // a DIFFERENT /64
	l := Wrap(&fakeListener{conns: []net.Conn{c1, c2, c3, c4}}, Config{MaxTotal: 100, MaxPerIP: 2})

	for i := 0; i < 2; i++ {
		_, err := l.Accept()
		require.NoError(t, err)
	}

	got, err := l.Accept()
	require.NoError(t, err)
	assert.Equal(t, c4.remote.String(), got.RemoteAddr().String(),
		"three addresses out of ONE /64 must count as one source: they cost their holder nothing, so a "+
			"/128 key makes the per-source cap unreachable and leaves the total cap as a shared bucket")
	assert.Equal(t, int32(1), c3.closes.Load())
	assert.Equal(t, uint64(1), l.Stats().Counts.RefusedPerIP,
		"the refusal must be ATTRIBUTED to the per-source cap, or the operator summary blames fleet growth")
	assert.Equal(t, int32(0), c4.closes.Load(),
		"a genuinely different /64 is a genuinely different source and must still be admitted")
}

// TestLimitListener_PerIPCapDoesNotAggregateIPv4 pins the other side of the
// asymmetry. IPv4 stays keyed on the exact /32: v4 addresses are scarce and
// already shared through NAT, so aggregating them would collapse unrelated
// operators onto one bucket. Prefix aggregation is a response to prefix
// DELEGATION, which has no v4 equivalent.
func TestLimitListener_PerIPCapDoesNotAggregateIPv4(t *testing.T) {
	c1 := newFakeConn("10.0.0.1:1001")
	c2 := newFakeConn("10.0.0.2:1002")
	c3 := newFakeConn("10.0.0.3:1003")
	l := Wrap(&fakeListener{conns: []net.Conn{c1, c2, c3}}, Config{MaxTotal: 100, MaxPerIP: 1})

	for i := 0; i < 3; i++ {
		_, err := l.Accept()
		require.NoError(t, err, "three DISTINCT v4 addresses are three distinct sources under a per-IP cap of 1")
	}
	assert.Equal(t, uint64(0), l.Stats().Counts.RefusedPerIP)
}

// textAddr is a net.Addr that renders EXACTLY the string it is given. hostKey
// reads a.String() and nothing else, so this is the only way to hand it a
// spelling that net.IP.String() would otherwise normalise away.
type textAddr string

func (a textAddr) Network() string { return "tcp" }
func (a textAddr) String() string  { return string(a) }

// TestLimitListener_IPv4MappedIPv6KeysAsIPv4 keeps a dual-stack listener from
// handing one host two buckets.
//
// THE LITERAL STRING FORM IS THE TEST. The &net.TCPAddr{IP:
// net.ParseIP("::ffff:10.0.0.1")} spelling this used to assert on is VACUOUS:
// net.IP.String() already renders a 16-byte v4-mapped address as "10.0.0.1", so
// hostKey's Unmap never sees a mapped address at all on that input, and DELETING
// the Unmap left the old assertion green - run and confirmed. The comment that
// went with it ("Go normalises this before it reaches hostKey, so this pins the
// Unmap") therefore described the reason the assertion could not pin anything.
//
// An addr whose String() IS the mapped literal is the discriminating input, and
// it is not exotic: hostKey takes whatever a net.Addr renders. With the Unmap it
// keys as 10.0.0.1; without it, ::ffff:10.0.0.1 aggregates to ::/64 and EVERY
// v4-mapped peer on the port collapses into that one bucket - a per-source cap
// that fires on unrelated hosts and a total cap any one of them can drain.
func TestLimitListener_IPv4MappedIPv6KeysAsIPv4(t *testing.T) {
	assert.Equal(t, "10.0.0.1", hostKey(textAddr("[::ffff:10.0.0.1]:99")),
		"a v4-mapped v6 source must key as the v4 address it is. Without the Unmap this is ::/64, which "+
			"is the bucket every OTHER v4-mapped peer also lands in")
	assert.Equal(t, hostKey(&net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1}),
		hostKey(textAddr("[::ffff:10.0.0.1]:99")),
		"a v4-mapped v6 peer is the same host as the v4 peer and must share its bucket")
}

// TestLimitListener_HostKeyDiscardsTheIPv6Zone pins a DISCLOSED imprecision, not
// a desired property, and exists so the disclosure in hostKey's comment cannot
// drift away from the code. netip.Prefix drops the zone (PrefixFrom calls
// withoutZone), so every link-local peer keys to fe80::/64 whatever interface it
// arrived on - and fe80::/64 is the entire link-local space, so the "smallest
// allocation anybody receives" argument for /64 does not hold there. A
// dual-homed server whose agents reach it over link-local on two separate LANs
// charges all of them to one 64-slot budget. Availability-only, and not a relay
// topology: agents dial a routable coordinator address.
func TestLimitListener_HostKeyDiscardsTheIPv6Zone(t *testing.T) {
	assert.Equal(t, "fe80::/64", hostKey(textAddr("[fe80::1%eth0]:99")))
	assert.Equal(t, hostKey(textAddr("[fe80::1%eth0]:99")), hostKey(textAddr("[fe80::1%eth1]:99")),
		"the zone is discarded, so two interfaces are one bucket - disclosed in hostKey, not fixed")
	assert.Equal(t, hostKey(textAddr("[fe80::1%eth0]:99")),
		hostKey(textAddr("[fe80::dead:beef:1:2%eth9]:99")))
}

// TestLimitListener_HostKeyFallsBackForANonIPAddress. Accept must key SOMETHING
// for a listener whose addresses are not IP - a Unix socket, or a test fake -
// rather than parsing its way to an empty key that silently merges every peer
// into one bucket.
func TestLimitListener_HostKeyFallsBackForANonIPAddress(t *testing.T) {
	assert.Equal(t, "/run/relay.sock", hostKey(&net.UnixAddr{Name: "/run/relay.sock", Net: "unix"}))
	assert.Equal(t, "", hostKey(nil))
	// An addr that renders no host at all keys the same as a nil addr. Not
	// reachable from a real listener; asserted so hostKey's enumeration of its
	// fallbacks is complete rather than nearly complete.
	assert.Equal(t, "", hostKey(&net.TCPAddr{IP: nil}))
}

// TestLimitListener_NilConnFromTheUnderlyingListenerIsSkipped.
//
// A net.Listener returning (nil, nil) is out of contract and no stdlib listener
// does it - but Wrap is EXPORTED, and netlimit must not be the place a
// misbehaving listener turns into a panic. grpc.Server.Serve does not recover
// its accept goroutine, so a panic here kills the process.
//
// NOT PANICKING HERE IS NOT ENOUGH, AND THE PREVIOUS VERSION OF THIS TEST
// ASSERTED EXACTLY THAT AND NO MORE. Handing the nil back to the caller only
// MOVES the panic one frame: grpc-go's handleRawConn calls rawConn.SetDeadline
// with no nil check, from a goroutine it does not recover
// (grpc@v1.80.0/server.go:960-974), so the process dies there instead of here.
// This test could not see that, because it never involves a grpc.Server. The
// nil must be SKIPPED instead - the accept loop already supports that, it is how
// a refused peer is handled - and the next real peer returned in its place.
//
// RED before the fix: Accept returns (nil, nil) and never reaches the real conn.
func TestLimitListener_NilConnFromTheUnderlyingListenerIsSkipped(t *testing.T) {
	real1 := newFakeConn("10.0.0.1:1001")
	l := Wrap(&fakeListener{conns: []net.Conn{nil, real1}}, Config{MaxTotal: 10, MaxPerIP: 10})

	got, err := l.Accept()
	require.NoError(t, err)
	require.NotNil(t, got,
		"a nil conn must be skipped, not handed on: grpc-go dereferences whatever Accept returns, from a "+
			"goroutine it does not recover, so passing the nil through relocates the panic rather than "+
			"preventing it")
	assert.Equal(t, real1.remote.String(), got.RemoteAddr().String(),
		"Accept must carry on to the next peer, exactly as it does for a refused one")

	l.mu.Lock()
	total, size := l.total, len(l.perIP)
	l.mu.Unlock()
	assert.Equal(t, 1, total, "the nil must consume no slot and the real conn must consume exactly one")
	assert.Equal(t, 1, size)
}

// TestLimitListener_BothCapsOffReturnsTheUnderlyingConnUnwrapped.
//
// README tells an operator fronting :9090 with a proxy to set both caps to 0.
// Wrapping is not free: Accept returns a WRAPPING net.Conn, and grpc-go's
// SetTCPUserTimeout type-asserts conn.(*net.TCPConn), so a wrapped conn silently
// loses TCP_USER_TIMEOUT on Linux (see this package's doc comment). With both
// caps disabled there is no slot to release, so the wrapper buys nothing and
// costs that. Hand the real conn back instead.
func TestLimitListener_BothCapsOffReturnsTheUnderlyingConnUnwrapped(t *testing.T) {
	inner := newFakeConn("10.0.0.1:1001")
	l := Wrap(&fakeListener{conns: []net.Conn{inner}}, Config{MaxTotal: 0, MaxPerIP: 0})

	got, err := l.Accept()
	require.NoError(t, err)
	assert.Same(t, inner, got,
		"with both caps disabled the accounting wrapper is pure cost: it cannot release a slot that was "+
			"never reserved, and it hides the concrete *net.TCPConn that grpc-go needs to set "+
			"TCP_USER_TIMEOUT on Linux")

	// One cap on is enough to need the wrapper back.
	inner2 := newFakeConn("10.0.0.1:1002")
	l2 := Wrap(&fakeListener{conns: []net.Conn{inner2}}, Config{MaxTotal: 0, MaxPerIP: 1})
	got2, err := l2.Accept()
	require.NoError(t, err)
	assert.NotSame(t, inner2, got2, "a live cap needs the wrapper, or Close can never release the slot")
}

// TestStats_ReportsOccupancy is the "how full is it right now" half of
// idea-2026-08-21-netlimit-occupancy-is-unobservable. Cumulative refusals cannot
// answer it: a RefusedTotal that stopped moving means either the pressure ended
// or the fleet settled at exactly the ceiling, and those need opposite
// responses.
//
// admit/release are driven directly rather than through Accept: they are the two
// critical sections Stats reads, and driving them straight makes the arithmetic
// the subject instead of the fake listener's plumbing.
func TestStats_ReportsOccupancy(t *testing.T) {
	l := Wrap(&fakeListener{}, Config{MaxTotal: 100, MaxPerIP: 100})
	require.True(t, l.admit("10.0.0.1"))
	require.True(t, l.admit("10.0.0.1"))
	require.True(t, l.admit("10.0.0.2"))

	s := l.Stats()
	assert.Equal(t, uint64(3), s.Levels.LiveTotal)
	assert.Equal(t, uint64(2), s.Levels.DistinctSources)
	assert.Equal(t, uint64(2), s.Levels.MaxPerSource, "10.0.0.1 holds two of the three")

	l.release("10.0.0.1")
	s = l.Stats()
	assert.Equal(t, uint64(2), s.Levels.LiveTotal, "a released slot must lower the level")
	assert.Equal(t, uint64(2), s.Levels.DistinctSources, "10.0.0.1 still holds one, so it is still a source")
	assert.Equal(t, uint64(1), s.Levels.MaxPerSource, "both sources now hold one each")

	l.release("10.0.0.2")
	s = l.Stats()
	assert.Equal(t, uint64(1), s.Levels.LiveTotal)
	assert.Equal(t, uint64(1), s.Levels.DistinctSources, "an emptied source leaves the map entirely")
	assert.Equal(t, uint64(0), s.Counts.RefusedTotal, "nothing was refused; occupancy must not be confused with refusal")
}

// TestStats_DistinguishesDistributedFromNAT is the item's second acceptance
// bullet AND the detection story for the IPv6 delegation residual the admission
// slice disclosed and could not fix. A healthy fleet behind NAT is a few sources
// holding many connections each; a distributed source pattern is many sources
// holding one each. RefusedTotal cannot tell them apart, and neither can
// LiveTotal - arrangements (a) and (b) below have IDENTICAL LiveTotal.
func TestStats_DistinguishesDistributedFromNAT(t *testing.T) {
	admitN := func(t *testing.T, l *Listener, key string, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			require.True(t, l.admit(key), "admit %s #%d must not be refused by these caps", key, i)
		}
	}

	// (a) The NAT shape: one source holding 64.
	nat := Wrap(&fakeListener{}, Config{MaxTotal: 4096, MaxPerIP: 4096})
	admitN(t, nat, "10.0.0.1", 64)
	n := nat.Stats()
	assert.Equal(t, uint64(64), n.Levels.LiveTotal)
	assert.Equal(t, uint64(1), n.Levels.DistinctSources)
	assert.Equal(t, uint64(64), n.Levels.MaxPerSource)

	// (b) The distributed shape: 64 sources holding one each.
	dist := Wrap(&fakeListener{}, Config{MaxTotal: 4096, MaxPerIP: 4096})
	for i := 0; i < 64; i++ {
		admitN(t, dist, fmt.Sprintf("10.1.0.%d", i), 1)
	}
	d := dist.Stats()
	require.Equal(t, n.Levels.LiveTotal, d.Levels.LiveTotal,
		"the two shapes must be indistinguishable by total occupancy alone - that is the premise of this test")
	assert.Equal(t, uint64(64), d.Levels.DistinctSources)
	assert.Equal(t, uint64(1), d.Levels.MaxPerSource)

	// (c) The IPv6 delegation shape at relay's real defaults: 16 /64s x 64
	//     connections fills the 1024 fleet cap with NOTHING refused. This is the
	//     case the item's Notes section asks to be in the matrix.
	deleg := Wrap(&fakeListener{}, Config{MaxTotal: 1024, MaxPerIP: 64})
	for p := 0; p < 16; p++ {
		admitN(t, deleg, fmt.Sprintf("2001:db8:0:%x::/64", p), 64)
	}
	g := deleg.Stats()
	assert.Equal(t, uint64(1024), g.Levels.LiveTotal, "the fleet cap is exactly full")
	assert.Equal(t, uint64(16), g.Levels.DistinctSources)
	assert.Equal(t, uint64(64), g.Levels.MaxPerSource, "every source sits exactly on the per-source cap")
	assert.Equal(t, uint64(0), g.Counts.RefusedTotal,
		"nothing has been refused YET, which is exactly why the refusal counters cannot see this shape")
	assert.Equal(t, uint64(0), g.Counts.RefusedPerIP)

	// The seventeenth source - a legitimate agent - is now refused by the TOTAL cap.
	require.False(t, deleg.admit("2001:db8:0:ff::/64"))
	g = deleg.Stats()
	assert.Equal(t, uint64(1), g.Counts.RefusedTotal)
	assert.Equal(t, uint64(64), g.Levels.MaxPerSource, "a refusal must move no level at all")

	// (d) The UNEQUAL arrangement, and the busiest source is deliberately in the
	//     MIDDLE: a MaxPerSource implemented as "the first entry" or "the last
	//     entry" must not be able to pass by position. 1 + 7 + 2 = 10 live, 3
	//     sources, max 7 - four numbers, all different, so len(perIP) and
	//     LiveTotal are both visibly wrong answers.
	uneq := Wrap(&fakeListener{}, Config{MaxTotal: 100, MaxPerIP: 100})
	admitN(t, uneq, "10.2.0.1", 1)
	admitN(t, uneq, "10.2.0.2", 7)
	admitN(t, uneq, "10.2.0.3", 2)
	u := uneq.Stats()
	assert.Equal(t, uint64(10), u.Levels.LiveTotal)
	assert.Equal(t, uint64(3), u.Levels.DistinctSources)
	assert.Equal(t, uint64(7), u.Levels.MaxPerSource,
		"MaxPerSource is the LARGEST per-source count, not the number of sources and not the total")
}

// TestStats_IsOneCriticalSection is the test the backlog item asks for by name,
// and its discriminating property is real rather than aspirational: with three
// separate lock acquisitions, connections being admitted between the reads make
// DistinctSources > LiveTotal directly observable.
//
// -race IS NOT THE INSTRUMENT HERE. The mutation this test exists to kill takes
// the lock three times instead of once, so every read is still properly
// synchronised and -race stays perfectly quiet under it. The INVARIANTS are the
// instrument.
//
// THE INVARIANT HALF NEEDS MORE THAN ONE CPU AND THE VACUITY HALF NO LONGER
// DOES, which is why they are made positive by different means. Measured: the
// three-lock mutation is caught 10/10 at default GOMAXPROCS (`[{2 3 1}]` - two
// live connections reported alongside three distinct sources) and 0/10 under
// -cpu=1, where the reader is never preempted between the three acquisitions.
// CI runs on a 2-4 vCPU runner, so the kill is live there; a 1-CPU cgroup would
// merely stop detecting the mutation rather than fail, now that the two "saw"
// counters are structural.
//
// The two "saw" counters are not decoration: a reader that only ever sampled an
// empty listener would satisfy every invariant vacuously, which is the recorded
// "measure the populated state" failure. They make the test fail when it proves
// nothing.
func TestStats_IsOneCriticalSection(t *testing.T) {
	const (
		sources = 128
		rounds  = 40
	)
	l := Wrap(&fakeListener{}, Config{MaxTotal: 100000, MaxPerIP: 100000})

	keys := make([]string, sources)
	for i := range keys {
		keys[i] = fmt.Sprintf("10.9.%d.%d", i/256, i%256)
	}

	// THE POPULATED STATE IS STRUCTURAL, NOT HOPED FOR. The two "saw" counters
	// below are the vacuity guard, and leaving them to interleaving made this
	// test fail 30/30 under GOMAXPROCS=1 and 6/6 under -cpu=1 ("412628
	// snapshots; 0 saw live connections"): the reader is an unsynchronised
	// busy-spin, so on one CPU the churn goroutine runs its entire loop inside a
	// single scheduling slice and every snapshot the reader takes is of an empty
	// listener. CI runs -race on a 2-4 vCPU runner and any 1-CPU cgroup fails it
	// every time. Admitting two sources BEFORE the churn starts and releasing
	// them after it finishes puts a floor of LiveTotal>=2, DistinctSources>=2
	// under every snapshot, so the guard is positive by construction while the
	// churn still supplies the interleaving the invariant needs.
	const (
		pinnedA = "10.8.0.1"
		pinnedB = "10.8.0.2"
	)
	require.True(t, l.admit(pinnedA))
	require.True(t, l.admit(pinnedB))

	stop := make(chan struct{})
	var churn sync.WaitGroup
	churn.Add(1)
	go func() {
		defer churn.Done()
		defer close(stop)
		for r := 0; r < rounds; r++ {
			for _, k := range keys {
				l.admit(k)
			}
			for _, k := range keys {
				l.release(k)
			}
		}
	}()

	var bad []Occupancy
	reads, sawLive, sawManySources := 0, 0, 0
	for done := false; !done; {
		select {
		case <-stop:
			done = true
		default:
		}
		s := l.Stats()
		reads++
		if s.Levels.LiveTotal > 0 {
			sawLive++
		}
		if s.Levels.DistinctSources > 1 {
			sawManySources++
		}
		if s.Levels.DistinctSources > s.Levels.LiveTotal || s.Levels.MaxPerSource > s.Levels.LiveTotal {
			bad = append(bad, s.Levels)
			done = true // one counter-example is the whole finding
		}
	}
	churn.Wait()
	l.release(pinnedA)
	l.release(pinnedB)

	t.Logf("%d snapshots; %d saw live connections; %d saw more than one source", reads, sawLive, sawManySources)
	require.Positive(t, sawLive,
		"the reader never observed a single live connection, so it proved nothing about a populated listener")
	require.Positive(t, sawManySources,
		"the reader never observed more than one source, so the DistinctSources invariant was never exercised")
	require.Empty(t, bad,
		"a snapshot reported more distinct sources (or a bigger per-source maximum) than it reported live "+
			"connections. That arrangement never existed: the numbers were read in separate critical sections "+
			"with connections opening and closing in between, and an operator would read it as a distributed "+
			"source pattern that is not there.")
}

// TestStats_ConcurrentRefusalsAndReadsShareTheMutex is the CONCURRENT EXPOSURE
// that makes -race able to see an unlocked counter, and that is its whole job.
//
// listener.go used to assert the coupling in prose: the refusal counters were
// atomics "INCREMENTED under this same mutex (see admit), so reading them here
// makes the whole five-field snapshot consistent". True, and nothing held it
// true - rewriting admit to decide over-cap under the lock, Unlock, then Add(1)
// outside left netlimit, cmd/relay-server AND internal/api all green, and a
// poller would then see refused_total climbing while live_total sat BELOW the
// configured cap, an arrangement the fleet was never in. The counters are now
// plain uint64 guarded by l.mu, so that refactor is a DATA RACE rather than a
// silent regression - but only if some test actually refuses connections while
// another goroutine reads Stats, and before this one none did.
//
// PROVED, exactly that way: with the split-increment mutation applied,
// `go test ./internal/netlimit/` is ok and
// `go test -race ./internal/netlimit/` reports WARNING: DATA RACE and fails
// here.
//
// THE INLINE INVARIANT IS A CHEAP CONSISTENCY CHECK, NOT THE KILL, and saying so
// is the point: nothing is released, so LiveTotal is pinned at the cap and
// "refusals reported alongside a listener that is not full" cannot arise from a
// split read either. The discriminating kills for miscounting a refusal live in
// TestLimitListener_RefusesBeyondPerIPCap, TestLimitListener_TotalCapRefuses-
// AcrossDistinctIPs, TestLimitListener_PerIPRefusalConsumesNoTotalSlot and
// TestStats_ReportsOccupancy, all four of which go RED on an unconditional
// increment. Do not read the require.Empty below as covering that.
//
// The populated state is structural, for the reason recorded on
// TestStats_IsOneCriticalSection: the listener is filled and one refusal forced
// BEFORE the reader starts, so sawRefusals cannot be zero however the scheduler
// behaves - including at GOMAXPROCS=1.
func TestStats_ConcurrentRefusalsAndReadsShareTheMutex(t *testing.T) {
	const maxTotal = 8
	l := Wrap(&fakeListener{}, Config{MaxTotal: maxTotal})

	for i := 0; i < maxTotal; i++ {
		require.True(t, l.admit(fmt.Sprintf("10.6.0.%d", i)))
	}
	require.False(t, l.admit("10.6.9.9"), "the fleet cap must already be reached before the reader starts")

	stop := make(chan struct{})
	var writers sync.WaitGroup
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; i < 2000; i++ {
				// Every one of these is refused: the total cap is full and
				// nothing releases.
				l.admit(fmt.Sprintf("10.7.%d.%d", w, i%256))
			}
		}(w)
	}
	go func() { writers.Wait(); close(stop) }()

	var bad []Stats
	reads, sawRefusals := 0, 0
	for done := false; !done; {
		select {
		case <-stop:
			done = true
		default:
		}
		s := l.Stats()
		reads++
		if s.Counts.RefusedTotal == 0 {
			continue
		}
		sawRefusals++
		if s.Levels.LiveTotal != maxTotal {
			bad = append(bad, s)
			done = true // one counter-example is the whole finding
		}
	}
	writers.Wait()

	t.Logf("%d snapshots; %d saw refusals", reads, sawRefusals)
	require.Positive(t, sawRefusals, "the reader never observed a refusal, so it proved nothing")
	require.Empty(t, bad,
		"a snapshot reported refusals alongside a listener that is NOT full (%v). Nothing is ever "+
			"released here, so live_total cannot fall back below the cap: the count and the level were "+
			"read at different moments. An operator reads that pair as 'we are refusing while under the "+
			"cap', an arrangement the fleet was never in.", bad)
}

// TestStats_CarriesNoIdentifiers answers "which IP is it?" NO, on the record and
// in code rather than in a comment. The refusal path is reachable by any
// unauthenticated peer and this type is rendered into a periodic log line, so a
// string field here would be an attacker-writable log site inside the control
// that exists to bound attacker-driven log volume.
//
// The leaf-path assertion is what stops this being vacuous: a walk that visited
// nothing would satisfy the type check trivially, and a NotEmpty check with a
// stern message is not a check.
func TestStats_CarriesNoIdentifiers(t *testing.T) {
	st := reflect.TypeOf(Stats{})
	require.Equal(t, 2, st.NumField(),
		"Stats has exactly two halves, Counts and Levels. A field added directly to Stats is neither "+
			"monotonic nor current, so no reporter can classify it and the trigger rule has no answer for it.")

	var leaves []string
	for i := 0; i < st.NumField(); i++ {
		half := st.Field(i)
		require.Equal(t, reflect.Struct, half.Type.Kind(), "Stats.%s must be a struct half", half.Name)
		for j := 0; j < half.Type.NumField(); j++ {
			f := half.Type.Field(j)
			path := half.Name + "." + f.Name
			leaves = append(leaves, path)
			switch f.Type.Kind() {
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			default:
				t.Fatalf("netlimit.Stats.%s is a %s. Every field of this type must be an UNSIGNED INTEGER: "+
					"an address, a prefix, a hostname or any other caller-supplied byte reaches an "+
					"attacker-driven log site through the refusal summary. More numbers, never identifiers.",
					path, f.Type.Kind())
			}
		}
	}
	assert.ElementsMatch(t, []string{
		"Counts.RefusedTotal", "Counts.RefusedPerIP",
		"Levels.LiveTotal", "Levels.DistinctSources", "Levels.MaxPerSource",
	}, leaves,
		"the field set of netlimit.Stats changed. Adding a number is fine - update this list deliberately - "+
			"but the list is here so the addition is a decision rather than a diff nobody read.")
}
