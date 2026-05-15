// Package metrics holds short-term, in-memory worker utilization telemetry and
// derives worker liveness from how recently each worker reported a sample.
package metrics

import (
	"sync"
	"time"
)

// DefaultSampleInterval is the assumed agent sampling cadence. The server uses
// it to size ring buffers and to report sample_interval_seconds to API clients.
// It is intentionally a constant, not configurable: the agent's actual cadence
// (RELAY_TELEMETRY_INTERVAL) may differ, which only shifts how much wall-clock
// history a fixed-size buffer holds.
const DefaultSampleInterval = 10 * time.Second

// Sample is one host-utilization reading for a worker, stamped with the
// server's receipt time.
type Sample struct {
	At             time.Time
	CPUPercent     float64
	MemUsedBytes   uint64
	MemTotalBytes  uint64
	HasGPU         bool
	GPUUtilPercent float64
	GPUMemUsed     uint64
	GPUMemTotal    uint64
}

// ring is one worker's bounded sample history.
type ring struct {
	activatedAt time.Time
	samples     []Sample // oldest-first; len <= capacity
}

// Store holds a bounded ring buffer of utilization samples per worker. All
// methods are safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	capacity int
	workers  map[string]*ring
}

// NewStore returns a Store whose per-worker buffers hold at most capacity
// samples. capacity is clamped to a minimum of 1.
func NewStore(capacity int) *Store {
	if capacity < 1 {
		capacity = 1
	}
	return &Store{capacity: capacity, workers: make(map[string]*ring)}
}

// Activate begins tracking a worker, seeding an empty buffer with the given
// activation time. Called when a worker registers.
func (s *Store) Activate(workerID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[workerID] = &ring{activatedAt: now}
}

// Append records a sample for a worker. It is a no-op if the worker is not
// currently tracked (i.e. Activate has not been called, or Clear has).
func (s *Store) Append(workerID string, sample Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.workers[workerID]
	if !ok {
		return
	}
	r.samples = append(r.samples, sample)
	if len(r.samples) > s.capacity {
		r.samples = r.samples[len(r.samples)-s.capacity:]
	}
}

// Clear stops tracking a worker and discards its samples.
func (s *Store) Clear(workerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workers, workerID)
}

// Snapshot returns a copy of the worker's samples, oldest-first. It returns a
// non-nil empty slice for an unknown or sample-less worker.
func (s *Store) Snapshot(workerID string) []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.workers[workerID]
	if !ok || len(r.samples) == 0 {
		return []Sample{}
	}
	out := make([]Sample, len(r.samples))
	copy(out, r.samples)
	return out
}

// LastSampleAt returns the time of the worker's most recent sample, or its
// activation time if it has reported no samples yet. The bool is false if the
// worker is not tracked.
func (s *Store) LastSampleAt(workerID string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.workers[workerID]
	if !ok {
		return time.Time{}, false
	}
	if len(r.samples) == 0 {
		return r.activatedAt, true
	}
	return r.samples[len(r.samples)-1].At, true
}
