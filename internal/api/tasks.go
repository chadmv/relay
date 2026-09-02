package api

import (
	"errors"
	"net/http"
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

type logEntry struct {
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) handleGetTaskLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}

	// Verify the task exists before paginating its logs.
	if _, err := s.q.GetTask(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "get task failed")
		return
	}

	q, qerr := parseTaskLogQuery(r.URL.Query())
	if qerr != nil {
		writeError(w, http.StatusBadRequest, qerr.Error())
		return
	}

	var logs []store.TaskLog
	switch {
	case q.Order == taskLogOrderDesc && q.BeforeSeq > 0:
		logs, err = s.q.GetTaskLogsBeforePage(ctx, store.GetTaskLogsBeforePageParams{
			TaskID:    id,
			BeforeSeq: q.BeforeSeq,
			RowLimit:  q.Limit,
		})
	case q.Order == taskLogOrderDesc:
		logs, err = s.q.GetTaskLogsTailPage(ctx, store.GetTaskLogsTailPageParams{
			TaskID:   id,
			RowLimit: q.Limit,
		})
	default:
		logs, err = s.q.GetTaskLogsPage(ctx, store.GetTaskLogsPageParams{
			TaskID: id,
			ID:     q.SinceSeq,
			Limit:  q.Limit,
		})
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get task logs failed")
		return
	}

	total, err := s.q.CountTaskLogs(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count task logs failed")
		return
	}

	// Non-nil so an empty page marshals as [] rather than null.
	items := make([]logEntry, 0, len(logs))
	for _, l := range logs {
		items = append(items, logEntry{
			Seq:       l.ID,
			Stream:    l.Stream,
			Content:   l.Content,
			CreatedAt: l.CreatedAt.Time,
		})
	}

	// Each direction populates exactly one cursor and zeroes the other, so a
	// direction-confused client stops immediately instead of looping. A short
	// page has drained that direction, and 0 is never a valid seq.
	var nextSeq, prevSeq int64
	if len(items) > 0 && int32(len(items)) >= q.Limit {
		if q.Order == taskLogOrderDesc {
			prevSeq = items[0].Seq
		} else {
			nextSeq = items[len(items)-1].Seq
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"next_seq": nextSeq,
		"prev_seq": prevSeq,
		"total":    total,
	})
}
