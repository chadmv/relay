package api

import (
	"errors"
	"net/http"
	"time"

	"relay/internal/metrics"

	"github.com/jackc/pgx/v5"
)

type metricSampleResponse struct {
	T           time.Time `json:"t"`
	CPUPct      float64   `json:"cpu_pct"`
	MemUsed     uint64    `json:"mem_used"`
	MemTotal    uint64    `json:"mem_total"`
	GPU         bool      `json:"gpu"`
	GPUUtilPct  float64   `json:"gpu_util_pct"`
	GPUMemUsed  uint64    `json:"gpu_mem_used"`
	GPUMemTotal uint64    `json:"gpu_mem_total"`
}

type workerMetricsResponse struct {
	WorkerID              string                 `json:"worker_id"`
	SampleIntervalSeconds int                    `json:"sample_interval_seconds"`
	Samples               []metricSampleResponse `json:"samples"`
}

// buildWorkerMetricsResponse assembles the JSON payload from the ring buffer.
// A nil store or an untracked worker yields an empty (non-nil) sample slice.
func buildWorkerMetricsResponse(workerID string, store *metrics.Store) workerMetricsResponse {
	samples := []metricSampleResponse{}
	if store != nil {
		for _, s := range store.Snapshot(workerID) {
			samples = append(samples, metricSampleResponse{
				T:           s.At,
				CPUPct:      s.CPUPercent,
				MemUsed:     s.MemUsedBytes,
				MemTotal:    s.MemTotalBytes,
				GPU:         s.HasGPU,
				GPUUtilPct:  s.GPUUtilPercent,
				GPUMemUsed:  s.GPUMemUsed,
				GPUMemTotal: s.GPUMemTotal,
			})
		}
	}
	return workerMetricsResponse{
		WorkerID:              workerID,
		SampleIntervalSeconds: int(metrics.DefaultSampleInterval / time.Second),
		Samples:               samples,
	}
}

// handleGetWorkerMetrics serves a worker's short-term utilization history.
func (s *Server) handleGetWorkerMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	if _, err := s.q.GetWorker(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	writeJSON(w, http.StatusOK, buildWorkerMetricsResponse(uuidStr(id), s.Metrics))
}
