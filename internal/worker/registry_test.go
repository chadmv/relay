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

func TestRegistry_Unregister(t *testing.T) {
	r := worker.NewRegistry()
	s := &fakeSender{}
	r.Register("worker-1", s)
	r.Unregister("worker-1")
	err := r.Send("worker-1", &relayv1.CoordinatorMessage{})
	assert.Error(t, err)
}
