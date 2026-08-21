package worker

import (
	"testing"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturingSender struct{ sent []*relayv1.CoordinatorMessage }

func (c *capturingSender) Send(m *relayv1.CoordinatorMessage) error {
	c.sent = append(c.sent, m)
	return nil
}

// TestRegistry_SendCancel_BuildsTheCancelTaskPayload keeps CancelTask
// construction in one place. It mirrors SendEvictCommand exactly.
func TestRegistry_SendCancel_BuildsTheCancelTaskPayload(t *testing.T) {
	r := NewRegistry()
	s := &capturingSender{}
	r.Register("w1", s)

	require.NoError(t, r.SendCancel("w1", "t1", false))

	require.Len(t, s.sent, 1)
	ct := s.sent[0].GetCancelTask()
	require.NotNil(t, ct, "the payload must be a CancelTask")
	assert.Equal(t, "t1", ct.TaskId)
	assert.False(t, ct.Force)
}

// TestRegistry_SendCancel_UnconnectedWorkerIsAnError: the caller decides what to
// do about it. The watchdog ignores it - best-effort by construction.
func TestRegistry_SendCancel_UnconnectedWorkerIsAnError(t *testing.T) {
	assert.Error(t, NewRegistry().SendCancel("nobody", "t1", false))
}
