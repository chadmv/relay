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
// ITS WRITE IS FENCED ON THE GENERATION IT VALIDATED, because this is a
// read-then-write across two implicit transactions: q is pool-backed, so the
// LIST and every UPDATE are separate statements with anything at all allowed to
// happen in between. Single-process placement (see cmd/relay-server) keeps
// THIS server's tick out of that window and nothing else, and relay supports
// multiple replicas. RecordScheduledJobFailure therefore matches on job_spec,
// cron_expr and timezone, so a schedule repaired through another replica cannot
// have this pass's stale verdict stamped back onto it. A non-match is an
// expected outcome, not an error.
//
// A PER-ROW FAILURE MUST NOT STOP THE SERVER BOOTING, and it cannot. A per-row
// record failure is logged and the sweep continues, so one bad row costs one log
// line rather than the remaining schedules; the only returned error is the list
// query's, which the caller in cmd/relay-server logs as a warning. Converting an
// operator-visible schedule problem into a server that will not start would be
// strictly worse than the invisibility this sweep exists to fix.
//
// THAT IS NARROWER THAN "THIS SWEEP CANNOT STOP THE BOOT", which is what this
// comment used to claim, and the difference is the unbounded read in FRONT of
// the loop rather than anything inside it. ListEnabledScheduledJobs has no
// LIMIT, this function consumes it into one slice synchronously, and the caller
// runs it before srv.ListenAndServe() - so a large enough scheduled_jobs table
// delays or exhausts the boot, and the HTTP API an operator would use to delete
// the offending rows is exactly what never comes up. There is no per-user
// schedule quota and no rate limit on POST /v1/scheduled-jobs, so growing it is
// an ordinary authenticated user's privilege. Paging this sweep and the quota
// policy are the scope of
// docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md,
// not of this comment; what is fixed here is the claim.
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
		// THE THREE COLUMNS PASSED BACK ARE THE FENCE, and they are exactly the
		// three validateStoredRow read. The write lands only if the row is still
		// the generation this verdict is about; see RecordScheduledJobFailure's
		// own header for why the fence is the content rather than updated_at.
		n, err := q.RecordScheduledJobFailure(ctx, store.RecordScheduledJobFailureParams{
			ID:        row.ID,
			LastError: &text,
			JobSpec:   row.JobSpec,
			CronExpr:  row.CronExpr,
			Timezone:  row.Timezone,
		})
		if err != nil {
			log.Printf("schedrunner: startup validation record for %s: %v", row.Name, err)
			continue
		}
		if n == 0 {
			// EITHER the row changed between the LIST and this UPDATE - another
			// replica, or an operator repairing it through one - OR the identical
			// message is already recorded. Neither is news, and the second is the
			// steady state for the entire life of a broken schedule, so logging
			// here would put a line in every boot's output forever.
			//
			// THIS IS THE WHOLE REASON THE QUERY IS :execrows. With :exec a fence
			// that said no and a database fault are the same nil, and the branch
			// below could not exist.
			continue
		}
		log.Printf("schedrunner: startup validation recorded a new failure for schedule %s: %s", row.Name, text)
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
