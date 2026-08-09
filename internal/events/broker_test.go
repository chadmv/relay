package events_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"relay/internal/events"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroker_PublishToAllSubscribers(t *testing.T) {
	b := events.NewBroker()

	ch1, cancel1 := b.Subscribe(events.Filter{})
	ch2, cancel2 := b.Subscribe(events.Filter{})
	defer cancel1()
	defer cancel2()

	e := events.Event{Type: "task", JobID: "job-1", Data: []byte(`{}`)}
	b.Publish(e)

	assert.Equal(t, e, <-ch1)
	assert.Equal(t, e, <-ch2)
}

func TestBroker_JobIDFilter(t *testing.T) {
	b := events.NewBroker()

	chAll, cancelAll := b.Subscribe(events.Filter{})
	chJob1, cancelJob1 := b.Subscribe(events.Filter{JobID: "job-1"})
	chJob2, cancelJob2 := b.Subscribe(events.Filter{JobID: "job-2"})
	defer cancelAll()
	defer cancelJob1()
	defer cancelJob2()

	b.Publish(events.Event{Type: "task", JobID: "job-1", Data: []byte(`{}`)})

	require.Equal(t, "job-1", (<-chAll).JobID)
	require.Equal(t, "job-1", (<-chJob1).JobID)

	select {
	case <-chJob2:
		t.Fatal("job-2 subscriber should not receive a job-1 event")
	case <-time.After(10 * time.Millisecond):
	}
}

func TestBroker_Cancel(t *testing.T) {
	b := events.NewBroker()

	ch, cancel := b.Subscribe(events.Filter{})
	cancel()

	b.Publish(events.Event{Type: "task", Data: []byte(`{}`)})

	select {
	case e, ok := <-ch:
		// Channel is closed on cancel — that is expected. A live event
		// delivered after cancellation is not.
		if ok {
			t.Fatalf("cancelled subscriber should not receive events: got %+v", e)
		}
	case <-time.After(10 * time.Millisecond):
	}
}

func TestBroker_SlowSubscriberIsDroppedAndClosed(t *testing.T) {
	b := events.NewBroker()

	slow, _ := b.Subscribe(events.Filter{})
	// Do not read from slow. Fill the 64-slot buffer, then publish one more.
	for i := 0; i < 65; i++ {
		b.Publish(events.Event{Type: "task", Data: []byte(`{}`)})
	}

	// Drain what made it into the buffer, then confirm the channel is closed
	// (reads return the zero value with ok=false).
	drained := 0
	closed := false
	deadline := time.After(time.Second)
	for !closed {
		select {
		case _, ok := <-slow:
			if !ok {
				closed = true
			} else {
				drained++
			}
		case <-deadline:
			t.Fatalf("slow subscriber channel was not closed; drained %d events", drained)
		}
	}
	assert.LessOrEqual(t, drained, 64, "drained more than buffer size before close")
}

func TestBroker_HealthySubscriberUnaffectedByDrop(t *testing.T) {
	b := events.NewBroker()

	slow, _ := b.Subscribe(events.Filter{})
	_ = slow // never read
	fast, cancelFast := b.Subscribe(events.Filter{})
	defer cancelFast()

	// Fire 65 events; the slow channel fills and gets dropped on event 65.
	// The fast subscriber must still receive all 65 events.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 65; i++ {
			<-fast
		}
	}()

	for i := 0; i < 65; i++ {
		b.Publish(events.Event{Type: "task", Data: []byte(`{}`)})
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fast subscriber did not receive all 65 events")
	}
}

func TestBroker_SubscribeTakesAFilterValue(t *testing.T) {
	b := events.NewBroker()

	// Filter{} is today's Subscribe("") - receives every status event.
	chAll, cancelAll := b.Subscribe(events.Filter{})
	defer cancelAll()
	// Filter{JobID: ...} is today's Subscribe("job-1").
	chJob1, cancelJob1 := b.Subscribe(events.Filter{JobID: "job-1"})
	defer cancelJob1()

	// A status event routes purely on JobID, so a TaskID on the event does not
	// narrow its delivery.
	e := events.Event{Type: "task", JobID: "job-1", TaskID: "task-1", Data: []byte(`{}`)}
	b.Publish(e)

	require.Equal(t, e, <-chAll)
	require.Equal(t, e, <-chJob1)
}

// --- Delivery matrix --------------------------------------------------------
// The four rows of the spec's matrix. A task_log event goes only to a
// subscription that named that exact TaskID; a status event goes to
// subscriptions where JobID == e.JobID, or where JobID == "" AND TaskID == "".

func logEvent(taskID string) events.Event {
	return events.Event{Type: events.TypeTaskLog, TaskID: taskID, JobID: "job-1", Data: []byte(`{"seq":1}`)}
}

func statusEvent(jobID string) events.Event {
	return events.Event{Type: "task", JobID: jobID, Data: []byte(`{}`)}
}

// mustNotReceive fails if ch holds a live event, or has been closed. Every
// caller performs its Publish calls synchronously on this goroutine first, and
// Publish delivers into the subscriber's buffer before it returns, so a
// non-blocking receive is exact: anything that was going to be delivered is
// already buffered. Deliberately not a time.After window - a wall-clock budget
// here would only add a way for a real delivery to be missed on a slow machine.
func mustNotReceive(t *testing.T, ch <-chan events.Event, what string) {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatalf("%s: channel was closed unexpectedly", what)
		}
		t.Fatalf("%s: unexpectedly received %+v", what, e)
	default:
	}
}

func TestBroker_GlobalSubscriberReceivesNoTaskLogEvents(t *testing.T) {
	b := events.NewBroker()
	chAll, cancel := b.Subscribe(events.Filter{})
	defer cancel()

	b.Publish(logEvent("task-1"))
	// Negative assertion. Paired positive control on the same code path and the
	// same channel: a status event MUST still arrive, so a broker that delivered
	// nothing at all could not pass this test.
	mustNotReceive(t, chAll, "global subscriber after a task_log publish")
	b.Publish(statusEvent("job-9"))
	require.Equal(t, "job-9", (<-chAll).JobID)
}

func TestBroker_TaskOnlySubscriberReceivesOnlyItsOwnLogs(t *testing.T) {
	b := events.NewBroker()
	ch, cancel := b.Subscribe(events.Filter{TaskID: "task-1"})
	defer cancel()

	b.Publish(statusEvent("job-1")) // must NOT arrive: TaskID-only is not a global status sub
	b.Publish(logEvent("task-2"))   // must NOT arrive: wrong task
	mustNotReceive(t, ch, "task-1 subscriber after a status publish and a task-2 log")

	// Positive control on the same channel.
	b.Publish(logEvent("task-1"))
	got := <-ch
	require.Equal(t, events.TypeTaskLog, got.Type)
	require.Equal(t, "task-1", got.TaskID)
}

func TestBroker_JobAndTaskSubscriberReceivesBoth(t *testing.T) {
	b := events.NewBroker()
	ch, cancel := b.Subscribe(events.Filter{JobID: "job-1", TaskID: "task-1"})
	defer cancel()

	b.Publish(statusEvent("job-2")) // wrong job
	b.Publish(logEvent("task-2"))   // wrong task
	mustNotReceive(t, ch, "{job-1,task-1} subscriber after a job-2 status and a task-2 log")

	b.Publish(statusEvent("job-1"))
	b.Publish(logEvent("task-1"))
	first := <-ch
	second := <-ch
	require.Equal(t, "task", first.Type)
	require.Equal(t, "job-1", first.JobID)
	require.Equal(t, events.TypeTaskLog, second.Type)
	require.Equal(t, "task-1", second.TaskID)
}

// --- The task-keyed index really is an index --------------------------------

func TestBroker_TaskLogPublishTouchesOnlyThatTasksSubscribers(t *testing.T) {
	b := events.NewBroker()
	logCh, cancelLog := b.Subscribe(events.Filter{TaskID: "task-1"})
	defer cancelLog()

	// 20 status subscribers, none of which reads.
	var statusChans []<-chan events.Event
	for i := 0; i < 20; i++ {
		ch, cancel := b.Subscribe(events.Filter{})
		defer cancel()
		statusChans = append(statusChans, ch)
	}

	// Positive control, and fully deterministic: Publish is synchronous, so the
	// event is already buffered on logCh by the time Publish returns.
	b.Publish(logEvent("task-1"))
	require.Equal(t, events.TypeTaskLog, (<-logCh).Type)

	// Now flood with nobody draining anything: 65 publishes against a 64-slot
	// buffer. If a task_log publish considered the status subscribers at all,
	// each would take a slot and be drop-closed on the 65th, which
	// mustNotReceive reports as "channel was closed unexpectedly". Deliberately
	// no drainer goroutine and no wall clock - every Publish below completes
	// before the assertions run, so the outcome cannot depend on scheduling.
	for i := 0; i < 65; i++ {
		b.Publish(logEvent("task-1"))
	}

	// Every status subscriber must still be open and empty.
	for i, ch := range statusChans {
		mustNotReceive(t, ch, "status subscriber "+strconv.Itoa(i))
	}

	// The bound applies to the log subscriber, and only to it: it stopped
	// reading, so it is the one that got dropped.
	require.False(t, b.HasLogSubscriber("task-1"),
		"the stalled log subscriber should have been drop-closed")
}

// --- HasLogSubscriber -------------------------------------------------------

func TestBroker_HasLogSubscriber(t *testing.T) {
	b := events.NewBroker()
	require.False(t, b.HasLogSubscriber("task-1"), "no subscribers")

	// A job-only subscription must NOT make it true - that is what keeps the
	// handleTaskLog fast path from marshalling for status-only watchers.
	_, cancelJob := b.Subscribe(events.Filter{JobID: "job-1"})
	require.False(t, b.HasLogSubscriber("task-1"), "job-only subscription")
	cancelJob()

	_, cancelTask := b.Subscribe(events.Filter{TaskID: "task-1"})
	require.True(t, b.HasLogSubscriber("task-1"))
	require.False(t, b.HasLogSubscriber("task-2"), "different task")
	cancelTask()
	require.False(t, b.HasLogSubscriber("task-1"), "after cancel")
}

// --- Both-indexes lifecycle: the panic surface ------------------------------

func TestBroker_CancelRemovesSubscriberFromBothIndexes(t *testing.T) {
	b := events.NewBroker()
	ch, cancel := b.Subscribe(events.Filter{JobID: "job-1", TaskID: "task-1"})

	cancel()
	// assert, not require: a stale index must not stop the two publishes below
	// from running. They are the lines that actually prove the panic surface is
	// closed, so short-circuiting here would leave them dead exactly when the
	// bug is present.
	assert.False(t, b.HasLogSubscriber("task-1"), "logSubs still holds the cancelled channel")

	// Neither publish may panic. A stale logSubs entry would be a
	// "send on closed channel" panic on the second line; a stale subs entry
	// would be one on the first.
	b.Publish(statusEvent("job-1"))
	b.Publish(logEvent("task-1"))

	// Cancel is idempotent: a second call must not double-close.
	cancel()

	_, ok := <-ch
	require.False(t, ok, "channel must be closed exactly once")
}

func TestBroker_DropViaStatusPublishRemovesFromBothIndexes(t *testing.T) {
	b := events.NewBroker()
	// Never read. Fill via STATUS events, which is the subs-index path.
	_, cancel := b.Subscribe(events.Filter{JobID: "job-1", TaskID: "task-1"})
	defer cancel()

	for i := 0; i < 65; i++ {
		b.Publish(statusEvent("job-1"))
	}
	// assert, not require - see TestBroker_CancelRemovesSubscriberFromBothIndexes.
	assert.False(t, b.HasLogSubscriber("task-1"),
		"a subscriber dropped by a status publish must be removed from logSubs too")

	// Would panic with "send on closed channel" if logSubs kept the entry.
	b.Publish(logEvent("task-1"))
}

func TestBroker_DropViaLogPublishRemovesFromBothIndexes(t *testing.T) {
	b := events.NewBroker()
	// Never read. Fill via LOG events, which is the logSubs-index path.
	_, cancel := b.Subscribe(events.Filter{JobID: "job-1", TaskID: "task-1"})
	defer cancel()

	for i := 0; i < 65; i++ {
		b.Publish(logEvent("task-1"))
	}
	assert.False(t, b.HasLogSubscriber("task-1"))

	// Would panic with "send on closed channel" if subs kept the entry.
	b.Publish(statusEvent("job-1"))
}

func TestBroker_StatusSubscriberUnaffectedByLogSubscriberDrop(t *testing.T) {
	b := events.NewBroker()
	// A log subscriber that never reads; it gets drop-closed on event 65.
	_, cancelSlow := b.Subscribe(events.Filter{TaskID: "task-1"})
	defer cancelSlow()
	fast, cancelFast := b.Subscribe(events.Filter{})
	defer cancelFast()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 65; i++ {
			<-fast
		}
	}()

	for i := 0; i < 65; i++ {
		b.Publish(logEvent("task-1"))   // drop-closes the slow log subscriber
		b.Publish(statusEvent("job-1")) // must still reach fast, all 65 times
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("status subscriber did not receive all 65 status events while a log subscriber was dropped")
	}
}

// --- The non-blocking guarantee, directly -----------------------------------

func TestBroker_PublishNeverBlocksOnAStalledLogSubscriber(t *testing.T) {
	b := events.NewBroker()
	// Deliberately no `defer cancel()`. If Publish ever blocks - the exact bug
	// this test exists to catch - the publishing goroutine is parked inside
	// Publish still holding b.mu, so a deferred cancel would block forever on
	// b.mu.Lock() and the whole package would time out instead of reporting this
	// test's failure. The broker is test-local, so leaking the subscription on
	// the failure path costs nothing, and on the success path the broker has
	// already closed and removed the channel itself.
	slow, _ := b.Subscribe(events.Filter{TaskID: "task-1"})

	// 200 publishes against a 64-slot buffer that nobody drains. Bounded, this
	// takes well under a millisecond; unbounded (a bare send) it blocks forever
	// on publish 65. The 5s budget is >1000x the real cost, so it cannot flake,
	// and the assertion is a Fatal on timeout rather than a hang because the
	// publishes run on their own goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			b.Publish(logEvent("task-1"))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a task-scoped subscriber that stopped reading")
	}

	// Drain to the close: the stalled subscriber must end up closed, not stuck.
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-slow:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("stalled log subscriber was never closed")
		}
	}
}

// --- Concurrency, for the race detector to chew on --------------------------

func TestBroker_ConcurrentSubscribeCancelPublish(t *testing.T) {
	b := events.NewBroker()
	var wg sync.WaitGroup

	// Churn subscriptions across both indexes.
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				f := events.Filter{}
				switch i % 3 {
				case 0:
					f = events.Filter{TaskID: "task-1"}
				case 1:
					f = events.Filter{JobID: "job-1", TaskID: "task-1"}
				}
				ch, cancel := b.Subscribe(f)
				go func() {
					for range ch {
					}
				}()
				cancel()
			}
		}(g)
	}
	// Publish both families concurrently.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				b.Publish(logEvent("task-1"))
				b.Publish(statusEvent("job-1"))
				_ = b.HasLogSubscriber("task-1")
			}
		}()
	}
	wg.Wait()
}
