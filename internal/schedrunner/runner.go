package schedrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"relay/internal/jobcreate"
	"relay/internal/jobspec"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TickInterval is how often the runner polls for eligible schedules.
const TickInterval = 10 * time.Second

// BatchLimit caps rows scanned per tick.
const BatchLimit = 100

// Runner owns the scheduled-job polling loop.
type Runner struct {
	pool *pgxpool.Pool
	q    *store.Queries
}

// NewRunner constructs a Runner.
func NewRunner(pool *pgxpool.Pool, q *store.Queries) *Runner {
	return &Runner{pool: pool, q: q}
}

// Run blocks until ctx is cancelled, ticking at TickInterval.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.TickOnce(ctx); err != nil {
				log.Printf("schedrunner tick: %v", err)
			}
		}
	}
}

// TickOnce performs one poll-and-fire cycle. Exposed for testing.
//
// All eligible rows in a tick share one outer transaction, but each row's fire
// runs inside its own savepoint (pgx nested tx). A failed fire rolls back only
// its savepoint, so a single poisoned schedule cannot abort the healthy rows'
// commits. The failed schedule's next_run_at is still advanced on the outer tx
// (without setting last_run_at) so it does not hot-loop every tick.
//
// A PERMANENT failure - an undecodable job_spec, an unparseable cron, or a spec
// that no longer passes jobspec.Validate - additionally records its message in
// last_error/last_error_at on that same outer tx. A transient one (a database
// fault) records nothing and preserves whatever was already there.
func (r *Runner) TickOnce(ctx context.Context) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := r.q.WithTx(tx)

	rows, err := q.ListEligibleScheduledJobs(ctx, BatchLimit)
	if err != nil {
		return err
	}
	for _, row := range rows {
		sp, err := tx.Begin(ctx)
		if err != nil {
			log.Printf("schedrunner: begin savepoint for %s: %v", row.Name, err)
			continue
		}
		next, fireErr := r.fireOne(ctx, r.q.WithTx(sp), row)
		if fireErr != nil {
			// Roll back ONLY this schedule's writes; the outer tx stays usable.
			if rbErr := sp.Rollback(ctx); rbErr != nil {
				log.Printf("schedrunner: rollback savepoint for %s: %v", row.Name, rbErr)
			}
			log.Printf("schedrunner: fire schedule %s: %v", row.Name, fireErr)

			// EVERY WRITE BELOW GOES ON THE OUTER TRANSACTION'S q, NEVER ON THE
			// SAVEPOINT'S. The savepoint was just rolled back - that rollback is
			// the entire point of the nested-transaction design, since it is what
			// stops one poisoned schedule aborting the healthy rows' commits. A
			// failure write issued inside fireOne would be discarded by it, and
			// discarded SILENTLY: the row would simply never carry an error and a
			// test would fail with no clue why. Do not move the classification or
			// the write into fireOne to "keep it together".
			//
			// fireErr itself is a Go value and is unaffected by the rollback, so
			// classifying it here is safe and is the only place it can be done.
			//
			// The row is held FOR UPDATE by the outer transaction for the whole
			// tick, so this write cannot race a concurrent PATCH: the PATCH blocks
			// on the row lock and the ordering is serialized by the database.
			if text, ok := recordableFailure(fireErr); ok {
				r.advanceAfterFailure(ctx, q, row, next, text)
			} else {
				// A transient infrastructure fault. Advance next_run_at so the
				// schedule does not hot-loop, and PRESERVE any existing record: a
				// blip is not news about the schedule, and overwriting a real
				// failure with a database hiccup would lose the only signal.
				r.advanceNextRun(ctx, q, row, next)
			}
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			log.Printf("schedrunner: release savepoint for %s: %v", row.Name, err)
		}
	}
	return tx.Commit(ctx)
}

// fireOne attempts to fire one schedule using q. On success it creates the job
// AND advances the schedule (last_run_at + last_job_id, and CLEARS any recorded
// failure) on q, then returns a nil error. On failure it returns the next_run_at
// the caller should advance to (without setting last_run_at) and a non-nil error.
// The caller is responsible for the savepoint and the failure-path advance on
// the outer tx.
//
// THE THREE PERMANENT FAILURES ARE WRAPPED IN permanent(). That marker is what
// TickOnce reads to decide whether the failure is a fact about the SCHEDULE
// (operator-supplied data, recorded) or about the INFRASTRUCTURE (a wrapped pgx
// error, logged only). See internal/schedrunner/failure.go for why the partition
// is worth making rather than recording everything uniformly.
func (r *Runner) fireOne(ctx context.Context, q *store.Queries, row store.ScheduledJob) (time.Time, error) {
	var spec jobspec.JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		return time.Now().Add(time.Minute), permanent(fmt.Errorf("invalid job_spec: %w", err))
	}
	sched, err := ParseSchedule(row.CronExpr, row.Timezone)
	if err != nil {
		return time.Now().Add(time.Minute), permanent(fmt.Errorf("parse cron: %w", err))
	}
	nextFire := sched.Next(time.Now())

	// Validate the STORED spec explicitly, here, ahead of the overlap check and
	// ahead of CreateJobFromSpec.
	//
	// WHY IT IS HOISTED. CreateJobFromSpec validates too, but every error it
	// returns collapses into one "create job: %w", so a validation failure and an
	// insert failure are indistinguishable at the call site - and this slice has
	// to tell them apart, because one is a permanent fact about the schedule's
	// own data and the other is a transient infrastructure fault whose pgx text
	// must not be stored in a column the SPA, the CLI, the Python SDK and the MCP
	// server all render.
	//
	// IT IS THE PRECEDENT, NOT A NEW IDEA. handleRunScheduledJobNow already does
	// exactly this, for exactly this reason, and its comment says so at length.
	// It respects the Single job-spec pipeline invariant: this is the same
	// jobspec.Validate, not a parallel check. And it is idempotent -
	// normalizeTaskCommands sees hasCommand == false, hasCommands == true on the
	// second call and falls through without error.
	//
	// NOTE THE ORDERING CONSEQUENCE, TAKEN DELIBERATELY: a poisoned spec now
	// reports its validation error even when a previous run is still active,
	// where before it would have skipped silently. That is the correct order. A
	// spec that cannot produce a job is a fact about the schedule regardless of
	// what else is running.
	if err := jobspec.Validate(&spec); err != nil {
		return nextFire, permanent(err)
	}

	if row.OverlapPolicy == "skip" {
		active, err := q.CountActiveJobsForSchedule(ctx, row.ID)
		if err != nil {
			return nextFire, fmt.Errorf("count active jobs: %w", err)
		}
		if active > 0 {
			log.Printf("schedrunner: skipping schedule %s (previous run still active)", row.Name)
			r.advanceSkipped(ctx, q, row, nextFire)
			return nextFire, nil
		}
	}

	job, _, err := jobcreate.CreateJobFromSpec(ctx, q, spec, row.OwnerID, row.ID)
	if err != nil {
		return nextFire, fmt.Errorf("create job: %w", err)
	}
	r.advance(ctx, q, row, job.ID, nextFire)
	return nextFire, nil
}

// advance is the SUCCESS path only. AdvanceScheduledJob clears last_error and
// last_error_at, which is correct here and only here: a completed
// CreateJobFromSpec is the only event that proves the stored spec both validates
// and inserts.
func (r *Runner) advance(ctx context.Context, q *store.Queries, row store.ScheduledJob, newJobID pgtype.UUID, next time.Time) {
	if err := q.AdvanceScheduledJob(ctx, store.AdvanceScheduledJobParams{
		ID:        row.ID,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
		LastJobID: newJobID, // COALESCE in SQL preserves old value when this is invalid
	}); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJob for %s: %v", row.Name, err)
	}
}

// advanceSkipped is the overlap_policy = 'skip' path. It stamps last_run_at, as
// this path always has, and it DOES NOT CLEAR the failure record: the skip
// branch returns before jobspec.Validate runs, so it is no evidence the spec is
// valid. Clearing here would make a poisoned schedule with a long-running
// predecessor flicker between "failing" and "healthy".
func (r *Runner) advanceSkipped(ctx context.Context, q *store.Queries, row store.ScheduledJob, next time.Time) {
	if err := q.AdvanceScheduledJobSkipped(ctx, store.AdvanceScheduledJobSkippedParams{
		ID:        row.ID,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
	}); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJobSkipped for %s: %v", row.Name, err)
	}
}

// advanceAfterFailure records a PERMANENT fire failure and advances next_run_at.
//
// IT MUST ONLY EVER BE CALLED WITH THE OUTER TRANSACTION'S q. See TickOnce.
func (r *Runner) advanceAfterFailure(ctx context.Context, q *store.Queries, row store.ScheduledJob, next time.Time, text string) {
	if err := q.AdvanceScheduledJobAfterFailure(ctx, store.AdvanceScheduledJobAfterFailureParams{
		ID:        row.ID,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
		LastError: &text,
	}); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJobAfterFailure for %s: %v", row.Name, err)
	}
}

func (r *Runner) advanceNextRun(ctx context.Context, q *store.Queries, row store.ScheduledJob, next time.Time) {
	if err := q.AdvanceScheduledJobNextRun(ctx, store.AdvanceScheduledJobNextRunParams{
		ID:        row.ID,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
	}); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJobNextRun for %s: %v", row.Name, err)
	}
}

// ReconcileOnStartup advances next_run_at past any missed triggers for every
// enabled schedule, implementing the never-catch-up policy. Call after
// migrations but before Runner.Run() starts.
func ReconcileOnStartup(ctx context.Context, q *store.Queries) error {
	rows, err := q.ListOverdueScheduledJobsForCatchup(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, row := range rows {
		sched, err := ParseSchedule(row.CronExpr, row.Timezone)
		if err != nil {
			log.Printf("schedrunner: reconcile skip for %s: %v", row.Name, err)
			continue
		}
		next := sched.Next(now)
		if err := q.AdvanceScheduledJobNextRun(ctx, store.AdvanceScheduledJobNextRunParams{
			ID:        row.ID,
			NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
		}); err != nil {
			log.Printf("schedrunner: reconcile advance for %s: %v", row.Name, err)
		}
	}
	return nil
}
