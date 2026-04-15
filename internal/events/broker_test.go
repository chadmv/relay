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
	case <-ch:
		t.Fatal("cancelled subscriber should not receive events")
	case <-time.After(10 * time.Millisecond):
	}
}
