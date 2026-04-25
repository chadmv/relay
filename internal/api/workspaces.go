package api

import (
	"net/http"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
)

type workspaceJSON struct {
	SourceType   string    `json:"source_type"`
	SourceKey    string    `json:"source_key"`
	ShortID      string    `json:"short_id"`
	BaselineHash string    `json:"baseline_hash"`
	LastUsedAt   time.Time `json:"last_used_at"`
}

func (s *Server) handleListWorkerWorkspaces(w http.ResponseWriter, r *http.Request) {
	workerID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.q.ListWorkerWorkspaces(r.Context(), workerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]workspaceJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, workspaceJSON{
			SourceType:   row.SourceType,
			SourceKey:    row.SourceKey,
			ShortID:      row.ShortID,
			BaselineHash: row.BaselineHash,
			LastUsedAt:   row.LastUsedAt.Time,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleEvictWorkerWorkspace(w http.ResponseWriter, r *http.Request) {
	workerID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	shortID := r.PathValue("short_id")
	if shortID == "" {
		writeError(w, http.StatusBadRequest, "short_id is required")
		return
	}
	// Look up the workspace to get its source_type.
	rows, err := s.q.ListWorkerWorkspaces(r.Context(), workerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	var found *store.WorkerWorkspace
	for i := range rows {
		if rows[i].ShortID == shortID {
			found = &rows[i]
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	cmd := &relayv1.EvictWorkspaceCommand{
		SourceType: found.SourceType,
		ShortId:    shortID,
	}
	// Best-effort: if the worker isn't connected, log and return 202 anyway.
	// The eviction will be retried when the worker reconnects (or the age-sweeper handles it).
	if err := s.registry.SendEvictCommand(uuidStr(workerID), cmd); err != nil {
		// Worker offline — still return 202; sweeper will handle it.
		// Note: don't delete DB row here; agent confirms via WorkspaceInventoryUpdate.
	}
	w.WriteHeader(http.StatusAccepted)
}
