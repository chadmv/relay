package metrics

import (
	"context"
	"fmt"
	"log"
	"time"

	"relay/internal/events"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// SweepInterval is how often the Sweeper re-evaluates worker liveness.
const SweepInterval = 10 * time.Second

// sweepStore is the subset of *store.Queries the Sweeper needs. *store.Queries
// satisfies it; tests supply a fake.
type sweepStore interface {
	ListWorkersByLiveness(ctx context.Context) ([]store.Worker, error)
	SetWorkerStatus(ctx context.Context, arg store.SetWorkerStatusParams) error
}

// Sweeper flips connected workers between "online" and "stale" based on how
// recently they reported telemetry. It never requeues tasks — a stale worker
// is still connected; disconnect-driven requeue stays with worker.GraceRegistry.
type Sweeper struct {
	q          sweepStore
	broker     *events.Broker
	store      *Store
	staleAfter time.Duration
	now        func() time.Time // injectable clock; defaults to time.Now
}

// NewSweeper constructs a Sweeper. staleAfter is the maximum age of a worker's
// last sample before an online worker is marked stale.
func NewSweeper(q sweepStore, broker *events.Broker, st *Store, staleAfter time.Duration) *Sweeper {
	return &Sweeper{q: q, broker: broker, store: st, staleAfter: staleAfter, now: time.Now}
}

// Run blocks until ctx is cancelled, sweeping every SweepInterval.
func (s *Sweeper) Run(ctx context.Context) {
	t := time.NewTicker(SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.SweepOnce(ctx); err != nil {
				log.Printf("metrics sweeper: %v", err)
			}
		}
	}
}

// SweepOnce performs one liveness check over all online/stale workers.
func (s *Sweeper) SweepOnce(ctx context.Context) error {
	workers, err := s.q.ListWorkersByLiveness(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, w := range workers {
		id := uuidString(w.ID)
		lastAt, tracked := s.store.LastSampleAt(id)
		if !tracked {
			// No telemetry tracking for this worker (e.g. it disconnected
			// between the query and now). Leave its status alone.
			continue
		}
		age := now.Sub(lastAt)
		switch {
		case w.Status == "online" && age > s.staleAfter:
			s.transition(ctx, id, "stale")
		case w.Status == "stale" && age <= s.staleAfter:
			s.transition(ctx, id, "online")
		}
	}
	return nil
}

// transition persists a status change and broadcasts it over SSE.
func (s *Sweeper) transition(ctx context.Context, workerID, status string) {
	var id pgtype.UUID
	if err := id.Scan(workerID); err != nil {
		return
	}
	if err := s.q.SetWorkerStatus(ctx, store.SetWorkerStatusParams{ID: id, Status: status}); err != nil {
		log.Printf("metrics sweeper: set status %s for %s: %v", status, workerID, err)
		return
	}
	s.broker.Publish(events.Event{
		Type: "worker",
		Data: []byte(fmt.Sprintf(`{"id":%q,"status":%q}`, workerID, status)),
	})
}

// uuidString converts a pgtype.UUID to its canonical string representation,
// matching the key format used elsewhere (worker.finishRegister).
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
