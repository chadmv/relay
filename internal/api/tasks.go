package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"relay/internal/store"

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
		resp[i] = toTaskResponse(t, nil)
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

	writeJSON(w, http.StatusOK, toTaskResponse(task, nil))
}

func (s *Server) handleGetTaskLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}

	limit := int32(50)
	if v := r.URL.Query().Get("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "limit must be 1..200")
			return
		}
		limit = int32(n)
	}

	var sinceSeq int64
	if v := r.URL.Query().Get("since_seq"); v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "since_seq must be a non-negative integer")
			return
		}
		sinceSeq = n
	}

	logs, err := s.q.GetTaskLogsPage(ctx, store.GetTaskLogsPageParams{
		TaskID: id,
		ID:     sinceSeq,
		Limit:  limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get task logs failed")
		return
	}

	total, err := s.q.CountTaskLogs(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count task logs failed")
		return
	}

	type logEntry struct {
		Seq       int64     `json:"seq"`
		Stream    string    `json:"stream"`
		Content   string    `json:"content"`
		CreatedAt time.Time `json:"created_at"`
	}
	items := make([]logEntry, len(logs))
	var nextSeq int64
	for i, l := range logs {
		items[i] = logEntry{
			Seq:       l.ID,
			Stream:    l.Stream,
			Content:   l.Content,
			CreatedAt: l.CreatedAt.Time,
		}
		nextSeq = l.ID
	}
	if int32(len(items)) < limit {
		nextSeq = 0 // drained
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"next_seq": nextSeq,
		"total":    total,
	})
}
