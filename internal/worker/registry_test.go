package worker_test

import (
	"testing"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSender struct {
	sent []*relayv1.CoordinatorMessage
}

func (f *fakeSender) Send(msg *relayv1.CoordinatorMessage) error {
	f.sent = append(f.sent, msg)
	return nil
}

func TestRegistry_RegisterAndSend(t *testing.T) {
	r := worker.NewRegistry()
	s := &fakeSender{}
	r.Register("worker-1", s)

	msg := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{TaskId: "task-abc"},
		},
	}
	err := r.Send("worker-1", msg)
	require.NoError(t, err)
	require.Len(t, s.sent, 1)
	assert.Equal(t, "task-abc", s.sent[0].GetDispatchTask().TaskId)
}

func TestRegistry_SendUnknownWorker(t *testing.T) {
	r := worker.NewRegistry()
	err := r.Send("ghost", &relayv1.CoordinatorMessage{})
	assert.Error(t, err)
}

func TestRegistry_UnregisterIf(t *testing.T) {
	r := worker.NewRegistry()
	s := &fakeSender{}
	r.Register("worker-1", s)

	// The owning sender removes itself.
	removed := r.UnregisterIf("worker-1", s)
	assert.True(t, removed, "owning sender should remove its slot")

	err := r.Send("worker-1", &relayv1.CoordinatorMessage{})
	assert.Error(t, err, "slot must be empty after UnregisterIf by the owner")
}

func TestRegistry_UnregisterIf_ReplaceThenStaleTeardown(t *testing.T) {
	r := worker.NewRegistry()
	a := &fakeSender{}
	b := &fakeSender{}

	// A registers, then B reconnects and replaces A for the same worker.
	r.Register("worker-1", a)
	r.Register("worker-1", b)

	// Stale teardown from A must NOT remove B's slot.
	removedA := r.UnregisterIf("worker-1", a)
	assert.False(t, removedA, "stale sender A must not own the slot")

	// B is still reachable.
	msg := &relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_DispatchTask{
			DispatchTask: &relayv1.DispatchTask{TaskId: "task-b"},
		},
	}
	require.NoError(t, r.Send("worker-1", msg), "B must still be registered after stale A teardown")
	require.Len(t, b.sent, 1)
	assert.Equal(t, "task-b", b.sent[0].GetDispatchTask().TaskId)
	assert.Empty(t, a.sent, "stale A must never receive sends")

	// B's own teardown removes the slot.
	removedB := r.UnregisterIf("worker-1", b)
	assert.True(t, removedB, "B owns the slot and should remove it")
	assert.Error(t, r.Send("worker-1", msg), "slot must be empty after B teardown")
}
