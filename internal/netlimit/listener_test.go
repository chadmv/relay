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
