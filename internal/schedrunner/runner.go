package schedrunner

import (
	"context"
	"encoding/json"
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
// All rows in a tick share one transaction. This means a failed fire (e.g.
// DB error inside createJob) still advances next_run_at via the same tx,
// preventing indefinite hot-loop retries on a broken schedule. last_job_id
// is left unchanged on failure via COALESCE in AdvanceScheduledJob.
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
		r.fireOne(ctx, q, row)
	}
	return tx.Commit(ctx)
}

func (r *Runner) fireOne(ctx context.Context, q *store.Queries, row store.ScheduledJob) {
	var spec jobspec.JobSpec
	if err := json.Unmarshal(row.JobSpec, &spec); err != nil {
		log.Printf("schedrunner: schedule %s has invalid job_spec: %v", row.Name, err)
		r.advance(ctx, q, row, pgtype.UUID{}, time.Now().Add(time.Minute))
		return
	}
	sched, err := ParseSchedule(row.CronExpr, row.Timezone)
	if err != nil {
		log.Printf("schedrunner: schedule %s failed to parse cron: %v", row.Name, err)
		r.advance(ctx, q, row, pgtype.UUID{}, time.Now().Add(time.Minute))
		return
	}
	nextFire := sched.Next(time.Now())

	if row.OverlapPolicy == "skip" {
		active, err := q.CountActiveJobsForSchedule(ctx, row.ID)
		if err != nil {
			log.Printf("schedrunner: CountActiveJobsForSchedule for %s: %v", row.Name, err)
			return
		}
		if active > 0 {
			log.Printf("schedrunner: skipping schedule %s (previous run still active)", row.Name)
			r.advance(ctx, q, row, pgtype.UUID{}, nextFire)
			return
		}
	}

	job, _, err := jobcreate.CreateJobFromSpec(ctx, q, spec, row.OwnerID, row.ID)
	if err != nil {
		log.Printf("schedrunner: createJob failed for %s: %v", row.Name, err)
		r.advance(ctx, q, row, pgtype.UUID{}, nextFire)
		return
	}
	r.advance(ctx, q, row, job.ID, nextFire)
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
		if err := q.AdvanceScheduledJob(ctx, store.AdvanceScheduledJobParams{
			ID:        row.ID,
			NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
			LastJobID: pgtype.UUID{}, // unchanged via COALESCE
		}); err != nil {
			log.Printf("schedrunner: reconcile advance for %s: %v", row.Name, err)
		}
	}
	return nil
}
