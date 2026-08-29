package schedrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"relay/internal/jobspec"
	"relay/internal/store"
)

// ValidateStoredSpecsOnStartup re-validates every ENABLED schedule's stored spec
// once at boot and records a failure for each one that no longer passes. Call
// after migrations, beside ReconcileOnStartup, before Runner.Run().
//
// WHY IT EXISTS. jobspec.Validate's rules are retroactive over stored
// scheduled_jobs rows, and ReconcileOnStartup implements never-catch-up, so
// after the deploy that carries a new rule a schedule broken by it records
// nothing until its NEXT SCHEDULED FIRE. For @daily that is up to a day; for
// @monthly, up to a month. The population most likely to be broken right now is
// exactly the population of long-cadence schedules nobody has looked at
// recently, so without this the failure surface is empty precisely where it is
// needed most.
//
// IT IS RECORD-ONLY AND IT NEVER CLEARS. A spec that validates at boot has not
// been proven to fire - CreateJobFromSpec's insert could still fail - so
// clearing here would assert something this pass did not observe, and a stale
// failure record is the more conservative state to leave standing. Clearing
// stays the exclusive job of a successful fire (AdvanceScheduledJob) and of a
// PATCH that changed job_spec, cron_expr or timezone (UpdateScheduledJob).
//
// IT DOES NOT TOUCH next_run_at. RecordScheduledJobFailure writes the failure
// columns only. ReconcileOnStartup owns the never-catch-up advance and running
// two statements that both move next_run_at at boot would skip a fire.
//
// IT IS NOT THE SAME QUESTION AS ReconcileOnStartup'S OWN ParseSchedule FAILURE,
// which deliberately records nothing: when that one fails it logs and continues
// WITHOUT advancing next_run_at, so the row stays overdue and
// ListEligibleScheduledJobs picks it up on the very next tick at most 10 seconds
// later, where fireOne's own ParseSchedule failure records it. A write there
// would be redundant within ten seconds and would add a second code path to keep
// in step. This function is about schedules that are NOT overdue and are
// therefore seen by neither loop for a full cron period.
//
// IT MUST NOT BE ABLE TO STOP THE SERVER BOOTING, and it is written so that it
// cannot. A per-row record failure is logged and the sweep continues, so one bad
// row costs one log line rather than the remaining schedules; the only returned
// error is the list query's, which the caller in cmd/relay-server logs as a
// warning. Converting an operator-visible schedule problem into a server that
// will not start would be strictly worse than the invisibility this sweep exists
// to fix.
//
// Cost: one pass over N enabled schedules at boot, with no I/O per row beyond
// the read that lists them and one UPDATE per BROKEN row.
func ValidateStoredSpecsOnStartup(ctx context.Context, q *store.Queries) error {
	rows, err := q.ListEnabledScheduledJobs(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		text, ok := recordableFailure(validateStoredRow(row))
		if !ok {
			continue
		}
		if err := q.RecordScheduledJobFailure(ctx, store.RecordScheduledJobFailureParams{
			ID:        row.ID,
			LastError: &text,
		}); err != nil {
			log.Printf("schedrunner: startup validation record for %s: %v", row.Name, err)
		}
	}
	return nil
}

// validateStoredRow returns a permanent() error if the row's stored data cannot
// produce a job, or nil.
func validateStoredRow(row store.ScheduledJob) error {
	return ValidateStoredSchedule(row.JobSpec, row.CronExpr, row.Timezone)
}

// ValidateStoredSchedule reports whether a schedule's three stored inputs can
// still produce a job: the job_spec decodes, the cron and timezone parse, and
// the spec passes jobspec.Validate. It returns a permanent() error naming the
// first failure, or nil.
//
// IT IS THE ONE VALIDATOR FOR THE STORED ROW, and it is exported for a single
// caller outside this package: internal/api's handlePatchScheduledJob, which
// decides whether a PATCH may clear the failure record. That decision is only
// sound if it asks the SAME question in the SAME order as the two sites that
// WRITE the record - the sweep above and fireOne - because clearing on a
// verdict those two would disagree with is exactly the defect it exists to fix.
// A second copy in internal/api would be a parallel validation path, and one
// that could drift silently in either direction.
//
// THE ORDER IS PART OF THE CONTRACT, not an implementation detail. It matches
// fireOne's first three steps exactly (unmarshal, ParseSchedule, Validate), so
// a row that this function accepts is a row fireOne gets past those three, and
// the message a caller reads names the same first failure fireOne would record.
//
// IT DELIBERATELY DUPLICATES THOSE THREE STEPS rather than being called BY
// fireOne. fireOne needs the PARSED spec and the PARSED schedule for the work
// that follows; this needs only the verdict. A shared helper returning both
// would give fireOne a second return path to keep in step for no gain. What IS
// shared, and is the part that matters, is the permanent() vocabulary and
// recordableFailure's classification - so no site can disagree about what
// counts as recordable.
//
// IT DOES NOT CHECK ValidateMinInterval, on purpose. The minimum interval is an
// API admission policy, not a fireability fact: schedrunner fires a sub-30s
// cron perfectly well, and no failure class records one. Including it here
// would make a legacy row stored under an older minimum permanently
// unclearable through PATCH.
func ValidateStoredSchedule(jobSpecJSON []byte, cronExpr, timezone string) error {
	var spec jobspec.JobSpec
	if err := json.Unmarshal(jobSpecJSON, &spec); err != nil {
		return permanent(fmt.Errorf("invalid job_spec: %w", err))
	}
	if _, err := ParseSchedule(cronExpr, timezone); err != nil {
		return permanent(fmt.Errorf("parse cron: %w", err))
	}
	if err := jobspec.Validate(&spec); err != nil {
		return permanent(err)
	}
	return nil
}
