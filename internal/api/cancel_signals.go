package api

import (
	"sync"

	relayv1 "relay/internal/proto/relayv1"
)

// cancelSignal is one best-effort CancelTask to deliver to a connected agent.
type cancelSignal struct {
	workerID string
	taskID   string
	force    bool
}

// sendCancelSignals delivers each CancelTask to its worker. The sends are
// best-effort (the return value is ignored; an eventual disconnect cleans up
// regardless), so a failed send just means the agent already lost the task.
//
// The sends run concurrently and the call blocks until all complete. Each send
// is bounded by the worker sender's send timeout, so a job/worker with N tasks
// on one wedged worker bounds the caller to ~one send timeout instead of N x it.
// registry.Send is safe for concurrent use (it looks the worker up under its own
// lock and workerSender.Send is concurrency-safe).
func (s *Server) sendCancelSignals(cancels []cancelSignal) {
	var wg sync.WaitGroup
	for _, c := range cancels {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.registry.Send(c.workerID, &relayv1.CoordinatorMessage{
				Payload: &relayv1.CoordinatorMessage_CancelTask{
					CancelTask: &relayv1.CancelTask{
						TaskId: c.taskID,
						Force:  c.force,
					},
				},
			})
		}()
	}
	wg.Wait()
}
