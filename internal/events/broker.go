package events

import (
	"log"
	"sync"
)

// TypeTaskLog is the Event.Type of a single persisted task-log line. Unlike the
// status types ("task", "job", "worker") it is routed purely by Event.TaskID:
// only a subscription that named that exact task receives it. There is
// deliberately no job-wide or global log firehose - a Filter{} subscriber (which
// relay watch opens) must never be handed every log line on the cluster.
const TypeTaskLog = "task_log"

// Event is published on any state change and delivered to SSE subscribers.
type Event struct {
	Type   string // "task", "job", "worker", or TypeTaskLog
	JobID  string // status events: empty = broadcast to all; non-empty = scoped to that job
	TaskID string // TypeTaskLog events: the task whose log line this is. Empty on status events.
	Data   []byte // JSON-encoded payload
}

// Filter is a subscription's delivery scope, passed by value. The zero value
// receives every status event, which is the historical Subscribe("") behaviour.
//
// Delivery matrix:
//
//	JobID  TaskID  receives
//	""     ""      all status events
//	J      ""      status events for J
//	""     T       task_log events for T only
//	J      T       status events for J plus task_log events for T
type Filter struct {
	JobID  string
	TaskID string
}

// Broker fans out published events to all matching subscribers.
//
// Two indexes, one channel. subs is the presence authority: a channel is in
// logSubs only while it is also in subs, and removeLocked is the only place a
// subscriber channel is closed. That is what makes close-exactly-once and
// removal-from-both-maps a single invariant instead of two.
//
// The second index exists because log chunks raise publish frequency by orders
// of magnitude: a task_log publish must iterate only that task's subscribers,
// never the whole subscriber set, so it cannot slow status delivery.
type Broker struct {
	mu      sync.Mutex
	subs    map[chan Event]Filter              // channel -> its delivery scope
	logSubs map[string]map[chan Event]struct{} // task id -> channels tailing that task
}

// NewBroker returns a ready-to-use Broker.
func NewBroker() *Broker {
	return &Broker{
		subs:    make(map[chan Event]Filter),
		logSubs: make(map[string]map[chan Event]struct{}),
	}
}

// Subscribe registers a new subscriber and returns a receive channel and a
// cancel function. See Filter for the delivery matrix. The channel has a buffer
// of 64; if the buffer fills, the broker unsubscribes and closes the channel -
// consumers should treat channel close as "you fell behind, reconnect if you
// need more". Cancel is idempotent and removes the channel from both indexes.
func (b *Broker) Subscribe(f Filter) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = f
	if f.TaskID != "" {
		m := b.logSubs[f.TaskID]
		if m == nil {
			m = make(map[chan Event]struct{})
			b.logSubs[f.TaskID] = m
		}
		m[ch] = struct{}{}
	}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		b.removeLocked(ch)
		b.mu.Unlock()
	}
}

// HasLogSubscriber reports whether anyone is tailing taskID's logs. It is an
// O(1) map lookup under one uncontended mutex acquire, so the log-ingest path
// can skip marshalling and publishing entirely in the steady state where nobody
// is watching. Racing is benign: a false negative drops at most the chunks in
// flight while a subscriber was mid-Subscribe, and that subscriber's
// GET /v1/tasks/{id}/logs backfill covers them.
func (b *Broker) HasLogSubscriber(taskID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.logSubs[taskID]) > 0
}

// removeLocked removes ch from both indexes and closes it. It is the ONLY place
// a subscriber channel is closed, and it guards on presence in b.subs, so a
// double cancel, or a cancel racing a slow-subscriber drop, closes exactly once.
// b.mu must be held.
func (b *Broker) removeLocked(ch chan Event) {
	f, ok := b.subs[ch]
	if !ok {
		return // already removed and closed
	}
	delete(b.subs, ch)
	if f.TaskID != "" {
		if m := b.logSubs[f.TaskID]; m != nil {
			delete(m, ch)
			if len(m) == 0 {
				delete(b.logSubs, f.TaskID)
			}
		}
	}
	close(ch)
}

// Publish delivers e to the matching subscribers and never blocks: a subscriber
// whose 64-slot buffer is full costs one failed send and is then closed and
// removed from both indexes. Callers may therefore publish from a gRPC recv
// goroutine or an HTTP handler without risk of being stalled by a peer that
// stopped reading. Do not replace the select/default with a blocking send, a
// timeout, or an unbounded queue.
func (b *Broker) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var dropped []chan Event
	if e.Type == TypeTaskLog {
		// Task-keyed fan-out only, so log traffic can never drop-close a
		// status-only subscription - one that did not name a task is not in this
		// index at all.
		//
		// It CAN drop-close a {JobID, TaskID} subscription, and that ends its
		// status delivery too: one subscription is one channel with one 64-slot
		// buffer shared by both families. That coupling is inherent to letting a
		// page tail logs and watch job status over a single connection, which is
		// the shape the SPA job-detail view wants. Such a client must recover by
		// re-backfilling logs AND refetching job/task state.
		for ch := range b.logSubs[e.TaskID] {
			select {
			case ch <- e:
			default:
				dropped = append(dropped, ch)
			}
		}
	} else {
		for ch, f := range b.subs {
			// A TaskID-only subscription is a log tail, not a global status
			// stream. Without this clause "?task_id=" alone would silently
			// become an accidental all-jobs status subscription.
			if f.JobID == "" && f.TaskID != "" {
				continue
			}
			if f.JobID == "" || f.JobID == e.JobID {
				select {
				case ch <- e:
				default:
					dropped = append(dropped, ch)
				}
			}
		}
	}

	for _, ch := range dropped {
		b.removeLocked(ch)
		log.Printf("events: dropped slow subscriber (buffer full)")
	}
}
