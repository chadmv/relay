package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	tasks, err := s.q.ListTasksByJob(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list tasks failed")
		return
	}

	resp := make([]taskResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = toTaskResponse(t)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}

	task, err := s.q.GetTask(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "task not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db error")
		}
		return
	}

	writeJSON(w, http.StatusOK, toTaskResponse(task))
}

func (s *Server) handleGetTaskLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}

	logs, err := s.q.GetTaskLogs(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get task logs failed")
		return
	}

	type logEntry struct {
		Stream    string    `json:"stream"`
		Content   string    `json:"content"`
		CreatedAt time.Time `json:"created_at"`
	}

	resp := make([]logEntry, len(logs))
	for i, l := range logs {
		resp[i] = logEntry{
			Stream:    l.Stream,
			Content:   l.Content,
			CreatedAt: l.CreatedAt.Time,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
