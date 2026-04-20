package events

import (
	"log"
	"sync"
)

// Event is published on any state change and delivered to SSE subscribers.
type Event struct {
	Type  string // "task", "job", or "worker"
	JobID string // empty = broadcast to all; non-empty = scoped to that job
	Data  []byte // JSON-encoded payload
}

// Broker fans out published events to all matching subscribers.
type Broker struct {
	mu   sync.Mutex
	subs map[chan Event]string // channel → jobID filter ("" = receive all)
}

// NewBroker returns a ready-to-use Broker.
func NewBroker() *Broker {
	return &Broker{subs: make(map[chan Event]string)}
}

// Subscribe registers a new subscriber and returns a receive channel and a
// cancel function. jobID="" receives all events; any other value receives only
// events whose JobID matches. The channel has a buffer of 64; if the buffer
// fills, the broker unsubscribes and closes the channel — consumers should
// treat channel close as "you fell behind, reconnect if you need more".
func (b *Broker) Subscribe(jobID string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = jobID
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
		}
		b.mu.Unlock()
	}
}

// Publish sends e to all subscribers whose filter matches e.JobID. Subscribers
// whose buffers are full are dropped: their channels are closed and removed.
func (b *Broker) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var dropped []chan Event
	for ch, filter := range b.subs {
		if filter == "" || filter == e.JobID {
			select {
			case ch <- e:
			default:
				dropped = append(dropped, ch)
			}
		}
	}
	for _, ch := range dropped {
		delete(b.subs, ch)
		close(ch)
		log.Printf("events: dropped slow subscriber (buffer full)")
	}
}
