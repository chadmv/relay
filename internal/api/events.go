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

	// ?job_id= is still deliberately NOT VALIDATED: an unknown or unparseable
	// job id yields an open, permanently empty stream rather than a 4xx, and
	// that is an existing contract with existing clients (README.md, "Events",
	// and TestEvents_TaskIDValidation asserts the `not-a-uuid` case is not a
	// 400). The asymmetry with task_id is intentional and is about REJECTION
	// only - both parameters are canonicalised, task_id eleven lines above and
	// job_id here since 2026-08-30. See canonicalJobIDFilter for why the
	// unparseable case must pass through UNCHANGED rather than be rendered.
	jobID := canonicalJobIDFilter(r.URL.Query().Get("job_id"))

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

// canonicalJobIDFilter renders raw in the one spelling every publisher emits,
// and returns raw UNCHANGED when it is not a UUID this server accepts.
//
// The server accepts far more spellings than it emits. parseUUID is
// pgtype.UUID.Scan, which takes hex case-insensitively, takes the dashless
// 32-character form, and on the 36-byte form slices out indexes 8, 13, 18 and
// 23 WITHOUT EXAMINING THEM - so `7e660488_1234_4321_8888_abcdefabcdef` names
// the same job as the canonical spelling, and GET /v1/jobs/{id} answers 200 for
// it. uuidStr renders exactly one of those spellings, and every JobID-carrying
// broker.Publish in the tree builds its value with uuidStr over a pgtype.UUID
// read from the database. internal/events' filter is an exact string compare,
// so without this an accepted-but-non-canonical id subscribed to a filter
// nothing could ever match: an open, silently empty stream forever.
//
// THE err != nil GUARD IS THE WHOLE CORRECTNESS ARGUMENT, NOT NOISE. parseUUID
// returns pgtype.UUID{} on failure and uuidStr returns "" for an invalid UUID,
// and Filter{JobID: ""} is the broker's BROADCAST subscription - Publish's
// status branch delivers to every filter whose JobID is empty. Rendering
// unconditionally would therefore promote every typo'd ?job_id= from "one job,
// silently empty" into "every job on the cluster": a silent change of scope from
// what the caller wrote, and the one way this change can be worse than doing
// nothing. Gate the render on the parse having actually succeeded, the same
// shape as gating a write on a fence having actually matched.
// TestEvents_JobIDRejectedSpellingsAreNotCanonicalised is the test that dies
// when this guard is deleted, and it asserts SCOPE rather than absence of error
// because a fail-open here is an escalation, not a crash.
//
// The !u.Valid arm mirrors internal/cli/logs.go's canonicalJobID and is
// belt-and-braces against a pgx whose Scan reports success without setting
// Valid. It costs one comparison; being wrong costs the broadcast above.
func canonicalJobIDFilter(raw string) string {
	u, err := parseUUID(raw)
	if err != nil || !u.Valid {
		return raw
	}
	return uuidStr(u)
}
