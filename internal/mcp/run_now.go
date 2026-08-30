package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type runScheduleNowArgs struct {
	ScheduleID string `json:"schedule_id" jsonschema:"The scheduled job ID to trigger immediately."`
}

func (s *Server) registerRunNow() {
	addTool(s, &mcpsdk.Tool{
		Name:        "relay_run_schedule_now",
		Description: "Trigger a relay scheduled job to run immediately, outside its normal cron schedule.",
	}, s.callRunScheduleNow)
}

func (s *Server) callRunScheduleNow(ctx context.Context, args runScheduleNowArgs) (map[string]any, *ToolError) {
	if args.ScheduleID == "" {
		return nil, &ToolError{Code: "validation", Message: "schedule_id is required"}
	}

	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	path := fmt.Sprintf("/v1/scheduled-jobs/%s/run-now", args.ScheduleID)
	if err := s.do(ctx, "POST", path, nil, &resp); err != nil {
		return nil, labelStoredSpecFailure(MapError(err))
	}
	return map[string]any{"job_id": resp.ID, "status": resp.Status}, nil
}

// labelStoredSpecFailure moves a validation failure's message out of the field a
// model reads as the error itself and into the labelled shape
// untrustedOperatorText defines.
//
// WHY ONLY THE VALIDATION ARM. handleRunScheduledJobNow re-runs ValidateJobSpec
// against the STORED spec, so its 400 CAN carry a task name the schedule's owner
// chose - which, on someone else's schedule, an admin's model then reads while
// holding relay_delete_schedule. Every other status this endpoint returns is a
// fixed relay string, and labelling those would spend the signal: a label that
// appears on everything says nothing.
//
// THE MESSAGE FIELD BECOMES RELAY'S OWN SENTENCE. Leaving the prose in `message`
// as well would defeat the wrap, because `message` is the field a model reads
// first and a duplicate is an unlabelled copy.
//
// THE ACCEPTED FALSE POSITIVE. That endpoint emits three 400s - "invalid id",
// "stored job_spec is invalid", and ValidateJobSpec's message - and only the
// third can carry operator prose. It does not always: the job-level messages
// ("at least one task is required", "at most N tasks are allowed") interpolate
// nothing an operator wrote, which is a second and smaller instance of the same
// accepted false positive. A CLIENT CANNOT TELL THEM APART; they arrive as
// one status with a different string. The alternative is string-matching relay's
// own fixed messages from a client, which encodes a peer's internal branch
// structure and goes silently wrong the first time the server rewords one.
// Over-labelling two rare branches is the fail-safe direction; under-labelling
// the common one is not.
//
// The other spec-carrying tools are deliberately NOT wrapped. relay_submit_job,
// relay_create_schedule and relay_update_schedule echo a spec the CALLER just
// sent, so their 400 is the model's own user's text coming back, not another
// party's.
func labelStoredSpecFailure(terr *ToolError) *ToolError {
	if terr == nil || terr.Code != "validation" {
		return terr
	}
	return &ToolError{
		Code:      terr.Code,
		Message:   "the schedule's stored job_spec was rejected; the server's reason is operator-supplied text and is under \"untrusted\"",
		Hint:      terr.Hint,
		Untrusted: untrustedOperatorText(terr.Message),
	}
}
