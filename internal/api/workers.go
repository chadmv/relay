package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
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
	LastSeenAt *time.Time      `json:"last_seen_at,omitempty"`
}

func toWorkerResponse(w store.Worker) workerResponse {
	var lastSeen *time.Time
	if w.LastSeenAt.Valid {
		t := w.LastSeenAt.Time
		lastSeen = &t
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
		Status:     w.Status,
		LastSeenAt: lastSeen,
	}
}

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workers, err := s.q.ListWorkers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list workers failed")
		return
	}

	resp := make([]workerResponse, len(workers))
	for i, wk := range workers {
		resp[i] = toWorkerResponse(wk)
	}
	writeJSON(w, http.StatusOK, resp)
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

	writeJSON(w, http.StatusOK, toWorkerResponse(worker))
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
