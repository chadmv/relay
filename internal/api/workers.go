package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type workerResponse struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Hostname   string          `json:"hostname"`
	CpuCores   int32           `json:"cpu_cores"`
	RamGb      int32           `json:"ram_gb"`
	GpuCount   int32           `json:"gpu_count"`
	GpuModel   string          `json:"gpu_model"`
	Os         string          `json:"os"`
	MaxSlots   int32           `json:"max_slots"`
	Labels     json.RawMessage `json:"labels"`
	Status     string          `json:"status"`
	LastSeenAt   *time.Time      `json:"last_seen_at,omitempty"`
	LastSampleAt *time.Time      `json:"last_sample_at,omitempty"`
	DisabledAt   *time.Time      `json:"disabled_at,omitempty"`
}

// disableWorkerResponse is the body returned by the disable endpoint. It embeds
// workerResponse (its fields flatten into the JSON object) and adds the count of
// tasks that were requeued - always 0 in drain mode.
type disableWorkerResponse struct {
	workerResponse
	RequeuedTasks int `json:"requeued_tasks"`
}

func toWorkerResponse(w store.Worker) workerResponse {
	var lastSeen *time.Time
	if w.LastSeenAt.Valid {
		t := w.LastSeenAt.Time
		lastSeen = &t
	}
	// A disabled worker keeps its live liveness status internally, but the API
	// reports "disabled" so existing consumers that read only `status` treat it
	// as unavailable. `disabled_at` is also exposed so both states are visible.
	status := w.Status
	var disabledAt *time.Time
	if w.DisabledAt.Valid {
		t := w.DisabledAt.Time
		disabledAt = &t
		status = "disabled"
	}
	return workerResponse{
		ID:         uuidStr(w.ID),
		Name:       w.Name,
		Hostname:   w.Hostname,
		CpuCores:   w.CpuCores,
		RamGb:      w.RamGb,
		GpuCount:   w.GpuCount,
		GpuModel:   w.GpuModel,
		Os:         w.Os,
		MaxSlots:   w.MaxSlots,
		Labels:     rawJSON(w.Labels),
		Status:     status,
		LastSeenAt: lastSeen,
		DisabledAt: disabledAt,
	}
}

func workersRowKey(w store.Worker) (time.Time, pgtype.UUID) {
	return w.CreatedAt.Time, w.ID
}

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r)
	if !ok {
		return
	}

	rows, err := s.q.ListWorkersPage(ctx, store.ListWorkersPageParams{
		CursorSet: pp.Cursor.Set,
		CursorTs:  pp.CursorTs(),
		CursorID:  pp.Cursor.ID,
		PageLimit: pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list workers failed")
		return
	}
	total, err := s.q.CountWorkers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count workers failed")
		return
	}
	items, next := buildPage(rows, pp.Limit, toWorkerResponse, workersRowKey)
	if s.Metrics != nil {
		for i := range items {
			if at, ok := s.Metrics.LastSampleAt(items[i].ID); ok {
				items[i].LastSampleAt = &at
			}
		}
	}
	writeJSON(w, http.StatusOK, page[workerResponse]{Items: items, NextCursor: next, Total: total})
}

func (s *Server) handleGetWorker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	worker, err := s.q.GetWorker(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	resp := toWorkerResponse(worker)
	if s.Metrics != nil {
		if at, ok := s.Metrics.LastSampleAt(uuidStr(worker.ID)); ok {
			resp.LastSampleAt = &at
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUpdateWorker(w http.ResponseWriter, r *http.Request) {
	// Note: this is a read-modify-write without a transaction.
	// Concurrent PATCH requests could race; acceptable for v1 admin operations.
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}

	current, err := s.q.GetWorker(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	var body struct {
		Name     *string           `json:"name"`
		Labels   map[string]string `json:"labels"`
		MaxSlots *int32            `json:"max_slots"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Merge with current values
	name := current.Name
	if body.Name != nil {
		name = *body.Name
	}

	labelsJSON := current.Labels
	if body.Labels != nil {
		labelsJSON, err = json.Marshal(body.Labels)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "marshal labels failed")
			return
		}
	}

	maxSlots := current.MaxSlots
	if body.MaxSlots != nil {
		maxSlots = *body.MaxSlots
	}

	updated, err := s.q.UpdateWorker(ctx, store.UpdateWorkerParams{
		ID:       id,
		Name:     name,
		Labels:   labelsJSON,
		MaxSlots: maxSlots,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "update worker failed")
		return
	}

	writeJSON(w, http.StatusOK, toWorkerResponse(updated))
}

func (s *Server) handleDisableWorker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}
	requeue, _ := strconv.ParseBool(r.URL.Query().Get("requeue"))

	current, err := s.q.GetWorker(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "worker not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	// Already disabled: no-op. Do not re-stamp disabled_at or re-cancel tasks.
	if current.DisabledAt.Valid {
		writeJSON(w, http.StatusOK, disableWorkerResponse{
			workerResponse: toWorkerResponse(current),
		})
		return
	}

	var requeuedIDs []pgtype.UUID
	if requeue {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		defer tx.Rollback(ctx)
		q := s.q.WithTx(tx)

		// Set disabled_at first so a dispatcher woken by NotifyTaskSubmitted
		// already sees the worker as ineligible and won't re-dispatch to it.
		if _, err := q.DisableWorker(ctx, id); err != nil {
			writeError(w, http.StatusInternalServerError, "disable worker failed")
			return
		}
		requeuedIDs, err = q.RequeueWorkerTasksWithEpoch(ctx, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "requeue tasks failed")
			return
		}
		if err := q.NotifyTaskSubmitted(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
		if err := tx.Commit(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}

		// Tell the still-connected agent to kill the now-orphaned subprocesses.
		// Best-effort: a failed send just means the agent already lost the task.
		for _, tid := range requeuedIDs {
			_ = s.registry.Send(uuidStr(id), &relayv1.CoordinatorMessage{
				Payload: &relayv1.CoordinatorMessage_CancelTask{
					CancelTask: &relayv1.CancelTask{
						TaskId: uuidStr(tid),
						Force:  false,
					},
				},
			})
		}
	} else {
		if _, err := s.q.DisableWorker(ctx, id); err != nil {
			writeError(w, http.StatusInternalServerError, "disable worker failed")
			return
		}
	}

	updated, err := s.q.GetWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, disableWorkerResponse{
		workerResponse: toWorkerResponse(updated),
		RequeuedTasks:  len(requeuedIDs),
	})
}

func (s *Server) handleEnableWorker(w http.ResponseWriter, r *http.Request) {
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

	if _, err := s.q.EnableWorker(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "enable worker failed")
		return
	}
	// Wake the dispatcher so the re-enabled worker can pick up pending tasks
	// immediately rather than waiting for the next ticker cycle.
	if err := s.q.NotifyTaskSubmitted(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	updated, err := s.q.GetWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, toWorkerResponse(updated))
}
