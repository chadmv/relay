package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"relay/internal/events"

	"github.com/jackc/pgx/v5"
)

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Validate ?task_id= BEFORE touching the response headers, so an error can be
	// written as JSON by writeError instead of arriving inside a half-started
	// text/event-stream response.
	//
	// One GetTask per CONNECTION (never per chunk). It exists to stop the worst
	// UX failure of live tailing: a typo'd id yielding a stream that hangs open
	// forever, silently, looking like "the task produced no output".
	//
	// No ownership check, deliberately: GET /v1/events and
	// GET /v1/tasks/{id}/logs are both auth(...)-only with no per-owner gate, so
	// any authenticated user can already read any task's logs. Adding a live view
	// of data the same token already reads introduces no escalation, and gating
	// only here would accomplish nothing.
	var logTaskID string
	if raw := r.URL.Query().Get("task_id"); raw != "" {
		taskID, err := parseUUID(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid task id")
			return
		}
		if _, err := s.q.GetTask(ctx, taskID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "task not found")
			} else {
				writeError(w, http.StatusInternalServerError, "get task failed")
			}
			return
		}
		// Canonical lowercase-hex form, so the broker key matches the one
		// handleTaskLog derives from the chunk's task id.
		logTaskID = uuidStr(taskID)
	}

	// ?job_id= is deliberately NOT validated: an unknown job has always yielded
	// an open, permanently empty stream, and that is an existing contract with
	// existing clients. The asymmetry with task_id is intentional.
	jobID := r.URL.Query().Get("job_id")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := s.broker.Subscribe(events.Filter{JobID: jobID, TaskID: logTaskID})
	defer cancel()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	// Subscribe-then-flush: when the client's request returns 200 the
	// subscription is already live, which is what lets a consumer subscribe first
	// and then backfill via ?since_seq without a gap.
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			// The client went away. Nothing to tell it.
			return
		case e, ok := <-ch:
			if !ok {
				// The broker dropped us for falling behind. Say so explicitly:
				// without this frame a Go consumer sees StreamEvents return nil,
				// indistinguishable from a normal end of stream. Additive and
				// safe - clients switch on event type and ignore unknowns.
				// The recovery is to re-backfill from the last seq seen.
				fmt.Fprint(w, "event: dropped\ndata: {\"reason\":\"slow_consumer\"}\n\n")
				flusher.Flush()
				return
			}
			// Replace newlines in data to keep SSE frame valid.
			// Per SSE spec, each line in the data value needs its own "data:" prefix.
			dataStr := strings.ReplaceAll(string(e.Data), "\n", "\ndata: ")
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, dataStr)
			flusher.Flush()
		}
	}
}
