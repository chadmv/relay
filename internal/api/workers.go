package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

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
	RevokedAt    *time.Time      `json:"revoked_at,omitempty"`
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
	var revokedAt *time.Time
	if w.RevokedAt.Valid {
		t := w.RevokedAt.Time
		revokedAt = &t
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
		RevokedAt:  revokedAt,
	}
}

// workerStatsResponse is the fleet-wide summary returned by GET /v1/workers/stats.
// total is the sum of the four buckets; revoked workers are in no bucket and are
// therefore excluded from total.
type workerStatsResponse struct {
	Online   int64 `json:"online"`
	Stale    int64 `json:"stale"`
	Offline  int64 `json:"offline"`
	Disabled int64 `json:"disabled"`
	Total    int64 `json:"total"`
}

func (s *Server) handleWorkerStats(w http.ResponseWriter, r *http.Request) {
	counts, err := s.q.WorkerStatusCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "worker stats failed")
		return
	}
	writeJSON(w, http.StatusOK, workerStatsResponse{
		Online:   counts.Online,
		Stale:    counts.Stale,
		Offline:  counts.Offline,
		Disabled: counts.Disabled,
		Total:    counts.Online + counts.Stale + counts.Offline + counts.Disabled,
	})
}

var WorkersSortSpec = SortSpec{
	Default: "-created_at",
	Keys: map[string]SortKeyKind{
		"created_at":   SortKeyTimestamp,
		"name":         SortKeyText,
		"status":       SortKeyText,
		"last_seen_at": SortKeyTimestamp,
	},
}

// RevokedWorkersSortSpec drives GET /v1/workers/revoked. The endpoint is
// DESC-only; the revoked_at key exists solely so the "-revoked_at" default
// resolves in parseSort. handleListRevokedWorkers rejects an ascending request.
var RevokedWorkersSortSpec = SortSpec{
	Default: "-revoked_at",
	Keys: map[string]SortKeyKind{
		"revoked_at": SortKeyTimestamp,
	},
}

func workersRowKey(w store.Worker) (anySortVal, pgtype.UUID) {
	return w.CreatedAt.Time, w.ID
}

func workersRowKeyByRevoked(w store.Worker) (anySortVal, pgtype.UUID) {
	if !w.RevokedAt.Valid {
		return (*time.Time)(nil), w.ID
	}
	t := w.RevokedAt.Time
	return &t, w.ID
}

func workersRowKeyByLastSeen(w store.Worker) (anySortVal, pgtype.UUID) {
	if !w.LastSeenAt.Valid {
		return (*time.Time)(nil), w.ID
	}
	t := w.LastSeenAt.Time
	return &t, w.ID
}

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, WorkersSortSpec)
	if !ok {
		return
	}

	total, err := s.q.CountWorkers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count workers failed")
		return
	}

	var items []workerResponse
	var next string

	switch pp.Sort {
	case "-created_at":
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
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKey)

	case "created_at":
		rows, err := s.q.ListWorkersPageByCreatedAsc(ctx, store.ListWorkersPageByCreatedAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKey)

	case "-name":
		rows, err := s.q.ListWorkersPageByNameDesc(ctx, store.ListWorkersPageByNameDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, func(w store.Worker) (anySortVal, pgtype.UUID) {
			return w.Name, w.ID
		})

	case "name":
		rows, err := s.q.ListWorkersPageByNameAsc(ctx, store.ListWorkersPageByNameAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, func(w store.Worker) (anySortVal, pgtype.UUID) {
			return w.Name, w.ID
		})

	case "-status":
		rows, err := s.q.ListWorkersPageByStatusDesc(ctx, store.ListWorkersPageByStatusDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, func(w store.Worker) (anySortVal, pgtype.UUID) {
			return w.Status, w.ID
		})

	case "status":
		rows, err := s.q.ListWorkersPageByStatusAsc(ctx, store.ListWorkersPageByStatusAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, func(w store.Worker) (anySortVal, pgtype.UUID) {
			return w.Status, w.ID
		})

	case "-last_seen_at":
		rows, err := s.q.ListWorkersPageByLastSeenDesc(ctx, store.ListWorkersPageByLastSeenDescParams{
			CursorSet:    pp.Cursor.Set,
			CursorIsNull: pp.Cursor.IsNull,
			CursorTs:     pp.CursorTs(),
			CursorID:     pp.Cursor.ID,
			PageLimit:    pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKeyByLastSeen)

	case "last_seen_at":
		rows, err := s.q.ListWorkersPageByLastSeenAsc(ctx, store.ListWorkersPageByLastSeenAscParams{
			CursorSet:    pp.Cursor.Set,
			CursorIsNull: pp.Cursor.IsNull,
			CursorTs:     pp.CursorTs(),
			CursorID:     pp.Cursor.ID,
			PageLimit:    pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list workers failed")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKeyByLastSeen)

	default:
		panic("handleListWorkers: missing dispatch arm for sort key " + pp.Sort)
	}

	if s.Metrics != nil {
		for i := range items {
			if at, ok := s.Metrics.LastSampleAt(items[i].ID); ok {
				items[i].LastSampleAt = &at
			}
		}
	}
	writeJSON(w, http.StatusOK, page[workerResponse]{Items: items, NextCursor: next, Total: total})
}

// handleListRevokedWorkers lists workers with status 'revoked' for admin audit.
// Admin-only. Ordered revoked_at DESC NULLS LAST, id DESC. Revoked workers are
// excluded from every other list/stats endpoint; this is the only surface for them.
func (s *Server) handleListRevokedWorkers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, RevokedWorkersSortSpec)
	if !ok {
		return
	}

	// This endpoint is DESC-only (the SQL ordering is fixed). The sort spec
	// must list the revoked_at key so the "-revoked_at" default resolves, but
	// an explicit ascending request can't be honored by the fixed query, so
	// reject it rather than silently returning descending rows.
	if pp.Sort != "-revoked_at" {
		writeError(w, http.StatusBadRequest, "revoked workers can only be sorted by -revoked_at")
		return
	}

	total, err := s.q.CountRevokedWorkers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count revoked workers failed")
		return
	}

	rows, err := s.q.ListRevokedWorkersPage(ctx, store.ListRevokedWorkersPageParams{
		CursorSet:    pp.Cursor.Set,
		CursorIsNull: pp.Cursor.IsNull,
		CursorTs:     pp.CursorTs(),
		CursorID:     pp.Cursor.ID,
		PageLimit:    pp.Limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list revoked workers failed")
		return
	}
	items, next := buildPage(rows, pp.Limit, pp.Sort, toWorkerResponse, workersRowKeyByRevoked)
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
	if !readJSON(w, r, &body) {
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
		// The :execrows count is the atomic check-and-set: a zero count means a
		// concurrent request disabled the worker first, so roll back and return
		// the no-op response rather than requeueing tasks it already handled.
		n, err := q.DisableWorker(ctx, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "disable worker failed")
			return
		}
		if n == 0 {
			_ = tx.Rollback(ctx)
			refreshed, err := s.q.GetWorker(ctx, id)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "db error")
				return
			}
			writeJSON(w, http.StatusOK, disableWorkerResponse{
				workerResponse: toWorkerResponse(refreshed),
			})
			return
		}
		requeuedIDs, err = q.RequeueWorkerTasks(ctx, id)
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
		cancels := make([]cancelSignal, 0, len(requeuedIDs))
		for _, tid := range requeuedIDs {
			cancels = append(cancels, cancelSignal{
				workerID: uuidStr(id),
				taskID:   uuidStr(tid),
				force:    false,
			})
		}
		s.sendCancelSignals(cancels)
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

	n, err := s.q.EnableWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "enable worker failed")
		return
	}
	// Wake the dispatcher so the re-enabled worker can pick up pending tasks
	// immediately. Skip the notify when the worker was already enabled (n == 0)
	// to avoid a spurious dispatch cycle.
	if n > 0 {
		if err := s.q.NotifyTaskSubmitted(ctx); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	updated, err := s.q.GetWorker(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, toWorkerResponse(updated))
}
