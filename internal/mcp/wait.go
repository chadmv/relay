package mcp

import (
	"context"
	"fmt"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"relay/internal/relayclient"
)

const (
	defaultWaitTimeout = 60 * time.Second
	maxWaitTimeout     = 300 * time.Second
	defaultWaitPoll    = 2 * time.Second        // steady-state poll interval
	fastWaitPoll       = 500 * time.Millisecond // poll interval during the fast phase
	fastWaitCount      = 4                       // number of fast intervals before widening

	// maxConsecutiveWaitFailures bounds how many polls in a row may fail
	// transiently before the wait gives up and reports the last failure. It is
	// consecutive, not cumulative: a server that answers in between resets it, so
	// a flaky backend never exhausts it and a dead one is reported in about
	// maxConsecutiveWaitFailures poll intervals instead of at the deadline.
	maxConsecutiveWaitFailures = 5
)

// nextWaitInterval returns the inter-poll sleep for the given zero-based attempt.
// The first fastWaitCount sleeps are fast (catching sub-2s jobs within ~500 ms of
// completion); every sleep thereafter is the steady interval, so a long wait does
// not increase GET load beyond today's 2 s cadence.
func nextWaitInterval(attempt int) time.Duration {
	if attempt < fastWaitCount {
		return fastWaitPoll
	}
	return defaultWaitPoll
}

var terminalStatuses = map[string]bool{
	"done":      true,
	"failed":    true,
	"cancelled": true,
}

type waitForJobArgs struct {
	JobID          string `json:"job_id"          jsonschema:"The job ID to wait for."`
	TimeoutSeconds int    `json:"timeout_seconds" jsonschema:"Seconds to wait before returning (0=use default 60s, max 300)."`
}

func (s *Server) registerWait() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_wait_for_job",
		Description: "Poll a relay job until it reaches a terminal state (done, failed, cancelled) or the timeout elapses.",
	}, s.callWaitForJob)
}

func (s *Server) callWaitForJob(ctx context.Context, args waitForJobArgs) (map[string]any, *ToolError) {
	if args.JobID == "" {
		return nil, &ToolError{Code: "validation", Message: "job_id is required"}
	}

	// Determine timeout duration.
	timeout := defaultWaitTimeout
	if args.TimeoutSeconds != 0 {
		if args.TimeoutSeconds < 0 {
			return nil, &ToolError{Code: "validation", Message: "timeout_seconds must be non-negative"}
		}
		t := time.Duration(args.TimeoutSeconds) * time.Second
		if t > maxWaitTimeout {
			return nil, &ToolError{
				Code:    "validation",
				Message: fmt.Sprintf("timeout_seconds must be <= %d", int(maxWaitTimeout/time.Second)),
			}
		}
		timeout = t
	}

	// Determine poll interval. A non-zero s.waitPoll is a flat-interval override
	// (used by tests for determinism); zero means use the adaptive schedule.
	flatPoll := s.waitPoll

	deadline := time.Now().Add(timeout)
	path := fmt.Sprintf("/v1/jobs/%s", args.JobID)

	var lastResp map[string]any
	consecutiveFailures := 0
	for attempt := 0; ; attempt++ {
		var resp map[string]any
		if err := s.do(ctx, "GET", path, nil, &resp); err != nil {
			// A poll failure is not automatically the wait's answer. This tool
			// reads ONE field of this response, `status`, and a failure of
			// anything else in it says nothing about the job being waited on -
			// handleGetJob reads the task list on the same request and started
			// reporting that read's failure as a 500 on 2026-08-26, where it had
			// been a silently task-less 200 this loop never noticed. Ending a wait
			// that may have been polling for minutes on one of those leaves the
			// caller with nothing to do but start over.
			//
			// Only failures a later poll can outlive are tolerated. Everything
			// else - a job that does not exist, an id the server rejects, a token
			// that has expired, a permission the caller does not have - is as true
			// on the hundredth read as on the first, so it ends the wait at once.
			//
			// The partition itself is relayclient.ErrorIsTransient, below both
			// this loop and the identical decision `relay logs` makes about its
			// subscribe-time snapshot. It used to be a copy here that read
			// MapError's code; the two loops now share one, because two spellings
			// of one partition drift.
			terr := MapError(err)
			if !relayclient.ErrorIsTransient(err) {
				return nil, terr
			}
			consecutiveFailures++
			if consecutiveFailures >= maxConsecutiveWaitFailures {
				return nil, terr
			}
			// lastResp is deliberately NOT cleared: it is the last thing actually
			// known about the job, and it is what a timed_out answer reports.
			switch waitUntilNextPoll(ctx, attempt, flatPoll, deadline) {
			case waitPollCancelled:
				return nil, &ToolError{Code: "cancelled", Message: "context cancelled"}
			case waitPollDeadline:
				if lastResp == nil {
					// Nothing was ever learned about the job, so there is no state
					// to hand back and the failure is the whole answer.
					return nil, terr
				}
				return map[string]any{"timed_out": true, "last_state": lastResp}, nil
			}
			continue
		}
		consecutiveFailures = 0
		lastResp = resp
		status, _ := lastResp["status"].(string)
		if terminalStatuses[status] {
			return lastResp, nil
		}

		switch waitUntilNextPoll(ctx, attempt, flatPoll, deadline) {
		case waitPollCancelled:
			return nil, &ToolError{Code: "cancelled", Message: "context cancelled"}
		case waitPollDeadline:
			return map[string]any{
				"timed_out":  true,
				"last_state": lastResp,
			}, nil
		}
	}
}

// waitPollOutcome is what the sleep between two polls decided.
type waitPollOutcome int

const (
	waitPollContinue waitPollOutcome = iota
	waitPollDeadline
	waitPollCancelled
)

// waitUntilNextPoll sleeps until the next poll is due, the deadline arrives, or
// the context is cancelled. Both callers in the loop share it: the successful
// poll and the tolerated failure sleep on the same schedule and honour the same
// deadline, and a copy of this at each site is a pair that can drift.
func waitUntilNextPoll(ctx context.Context, attempt int, flatPoll time.Duration, deadline time.Time) waitPollOutcome {
	if !time.Now().Before(deadline) {
		return waitPollDeadline
	}
	poll := flatPoll
	if poll == 0 {
		poll = nextWaitInterval(attempt)
	}
	if remaining := time.Until(deadline); remaining < poll {
		poll = remaining
	}
	if poll <= 0 {
		return waitPollDeadline
	}
	select {
	case <-ctx.Done():
		return waitPollCancelled
	case <-time.After(poll):
		return waitPollContinue
	}
}
