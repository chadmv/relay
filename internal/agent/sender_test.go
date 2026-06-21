package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
)

// TestRunSender_AtMostOneSenderAcrossReconnect proves that joining the previous
// send goroutine (via a.sendWG) before spawning the next guarantees at most one
// runSender reads a.sendCh at any instant. It simulates a reconnect: sender #1
// is parked inside send() on a dying connection; the test then performs the
// same join connect() does (sendWG.Wait after connCancel) and only then starts
// sender #2. A maxConcurrent counter must never exceed 1.
func TestRunSender_AtMostOneSenderAcrossReconnect(t *testing.T) {
	a := &Agent{sendCh: make(chan *relayv1.AgentMessage, 64)}

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	bump := func() {
		n := concurrent.Add(1)
		for {
			m := maxConcurrent.Load()
			if n <= m || maxConcurrent.CompareAndSwap(m, n) {
				break
			}
		}
	}

	// send blocks until the test releases it, modelling a stream.Send parked on
	// a dead connection. The returned error makes runSender call connCancel and
	// exit (as a real send error does).
	entered := make(chan struct{})
	release := make(chan struct{})
	sendBlocking := func(*relayv1.AgentMessage) error {
		bump()
		defer concurrent.Add(-1)
		close(entered)
		<-release
		return errors.New("connection dead")
	}

	// Connection 1.
	ctx := context.Background()
	conn1Ctx, conn1Cancel := context.WithCancel(ctx)
	a.sendWG.Add(1)
	go a.runSender(conn1Ctx, conn1Cancel, sendBlocking)

	// Drive sender #1 into send(): enqueue one message and wait until it is
	// parked inside sendBlocking.
	a.sendCh <- &relayv1.AgentMessage{}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("sender #1 never entered send()")
	}

	// Simulate the recv loop dropping the stream: cancel conn1 (as connect's
	// defer connCancel / the recv-loop connCancel would).
	conn1Cancel()

	// Now perform the join exactly as connect() does at the top, BEFORE
	// starting connection 2. Sender #1 is still parked in send(), so this must
	// block until we release it - proving the join actually waits.
	joined := make(chan struct{})
	go func() {
		a.sendWG.Wait()
		close(joined)
	}()
	select {
	case <-joined:
		t.Fatal("sendWG.Wait returned while sender #1 was still inside send(); join did not wait")
	case <-time.After(100 * time.Millisecond):
		// Expected: still blocked because sender #1 has not returned.
	}

	// Release sender #1; it returns from send() (error path) and signals Done.
	close(release)
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("sendWG.Wait did not return after sender #1 exited")
	}

	// Only now start connection 2's sender, as connect() would post-Wait.
	conn2Ctx, conn2Cancel := context.WithCancel(ctx)
	defer conn2Cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	noopSend := func(*relayv1.AgentMessage) error { bump(); defer concurrent.Add(-1); return nil }
	a.sendWG.Add(1)
	go func() { defer wg.Done(); a.runSender(conn2Ctx, conn2Cancel, noopSend) }()

	// Push a couple of messages through sender #2.
	a.sendCh <- &relayv1.AgentMessage{}
	a.sendCh <- &relayv1.AgentMessage{}
	time.Sleep(100 * time.Millisecond)

	conn2Cancel()
	wg.Wait()
	a.sendWG.Wait()

	if got := maxConcurrent.Load(); got > 1 {
		t.Fatalf("at most one sender must read sendCh; observed %d concurrent", got)
	}
}

// TestRunSender_QueuedMessageDeliveredByLiveSender proves that after a drop,
// once the previous sender is joined, every message subsequently queued on
// sendCh is delivered exactly once by the new (live) sender - no message is
// stolen or dropped by a lingering old sender. It also documents the bounded
// loss: the single message already handed to a failing send is the only one
// that can be lost, and it is not redelivered (we do not re-enqueue).
func TestRunSender_QueuedMessageDeliveredByLiveSender(t *testing.T) {
	a := &Agent{sendCh: make(chan *relayv1.AgentMessage, 64)}
	ctx := context.Background()

	// Connection 1: send fails immediately on the first message (dead conn),
	// modelling the in-flight message that is dropped on a drop.
	var conn1Delivered atomic.Int32
	conn1Ctx, conn1Cancel := context.WithCancel(ctx)
	a.sendWG.Add(1)
	conn1Done := make(chan struct{})
	failingSend := func(*relayv1.AgentMessage) error {
		conn1Delivered.Add(1)
		return errors.New("connection dead")
	}
	go func() { defer close(conn1Done); a.runSender(conn1Ctx, conn1Cancel, failingSend) }()

	// Queue the in-flight message; sender #1 pulls it, send fails, sender #1
	// cancels its conn and exits. This message is the documented bounded loss.
	a.sendCh <- &relayv1.AgentMessage{}
	select {
	case <-conn1Done:
	case <-time.After(2 * time.Second):
		t.Fatal("sender #1 did not exit after send error")
	}

	// Join as connect() does before the next connection.
	a.sendWG.Wait()
	if got := conn1Delivered.Load(); got != 1 {
		t.Fatalf("sender #1 should have attempted exactly 1 send, got %d", got)
	}

	// Connection 2: live sender records what it delivers.
	var delivered []string
	var mu sync.Mutex
	conn2Ctx, conn2Cancel := context.WithCancel(ctx)
	defer conn2Cancel()
	a.sendWG.Add(1)
	liveSend := func(m *relayv1.AgentMessage) error {
		mu.Lock()
		delivered = append(delivered, m.GetTaskLog().GetTaskId())
		mu.Unlock()
		return nil
	}
	go a.runSender(conn2Ctx, conn2Cancel, liveSend)

	// Queue three post-reconnect messages; all must reach the live sender.
	for _, id := range []string{"a", "b", "c"} {
		a.sendCh <- &relayv1.AgentMessage{Payload: &relayv1.AgentMessage_TaskLog{
			TaskLog: &relayv1.TaskLogChunk{TaskId: id},
		}}
	}

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(delivered)
		mu.Unlock()
		if n == 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("live sender delivered %d/3 queued messages", n)
		case <-time.After(10 * time.Millisecond):
		}
	}

	conn2Cancel()
	a.sendWG.Wait()

	mu.Lock()
	defer mu.Unlock()
	for i, want := range []string{"a", "b", "c"} {
		if delivered[i] != want {
			t.Fatalf("queued message %d: got %q want %q (order or delivery wrong)", i, delivered[i], want)
		}
	}
}
