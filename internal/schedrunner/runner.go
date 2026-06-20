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
			// Advance next_run_at on the OUTER tx (no last_run_at) so the
			// poisoned schedule stops hot-looping every tick.
			r.advanceNextRun(ctx, q, row, next)
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			log.Printf("schedrunner: release savepoint for %s: %v", row.Name, err)
		}
	}
	return tx.Commit(ctx)
}

// fireOne attempts to fire one schedule using q. On success it creates the job
// AND advances the schedule (last_run_at + last_job_id) on q, then returns a nil
// error. On failure it returns the next_run_at the caller should advance to
// (without setting last_run_at) and a non-nil error. The caller is responsible
// for the savepoint and the failure-path advance on the outer tx.
func (r *Runner) fireOne(ctx context.Context, q *store.Queries, row store.ScheduledJob) (time.Time, error) {
	var spec jobspec.JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		return time.Now().Add(time.Minute), fmt.Errorf("invalid job_spec: %w", err)
	}
	sched, err := ParseSchedule(row.CronExpr, row.Timezone)
	if err != nil {
		return time.Now().Add(time.Minute), fmt.Errorf("parse cron: %w", err)
	}
	nextFire := sched.Next(time.Now())

	if row.OverlapPolicy == "skip" {
		active, err := q.CountActiveJobsForSchedule(ctx, row.ID)
		if err != nil {
			return nextFire, fmt.Errorf("count active jobs: %w", err)
		}
		if active > 0 {
			log.Printf("schedrunner: skipping schedule %s (previous run still active)", row.Name)
			r.advance(ctx, q, row, pgtype.UUID{}, nextFire)
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

func (r *Runner) advance(ctx context.Context, q *store.Queries, row store.ScheduledJob, newJobID pgtype.UUID, next time.Time) {
	if err := q.AdvanceScheduledJob(ctx, store.AdvanceScheduledJobParams{
		ID:        row.ID,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
		LastJobID: newJobID, // COALESCE in SQL preserves old value when this is invalid
	}); err != nil {
		log.Printf("schedrunner: AdvanceScheduledJob for %s: %v", row.Name, err)
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
