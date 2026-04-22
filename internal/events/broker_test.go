package events_test

import (
	"testing"
	"time"

	"relay/internal/events"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroker_PublishToAllSubscribers(t *testing.T) {
	b := events.NewBroker()

	ch1, cancel1 := b.Subscribe("")
	ch2, cancel2 := b.Subscribe("")
	defer cancel1()
	defer cancel2()

	e := events.Event{Type: "task", JobID: "job-1", Data: []byte(`{}`)}
	b.Publish(e)

	assert.Equal(t, e, <-ch1)
	assert.Equal(t, e, <-ch2)
}

func TestBroker_JobIDFilter(t *testing.T) {
	b := events.NewBroker()

	chAll, cancelAll := b.Subscribe("")
	chJob1, cancelJob1 := b.Subscribe("job-1")
	chJob2, cancelJob2 := b.Subscribe("job-2")
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

	ch, cancel := b.Subscribe("")
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

	slow, _ := b.Subscribe("")
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

	slow, _ := b.Subscribe("")
	_ = slow // never read
	fast, cancelFast := b.Subscribe("")
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
