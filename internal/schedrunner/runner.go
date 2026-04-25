package schedrunner

import (
	"context"
	"encoding/json"
	"log"
	"time"

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

// runnerSpec mirrors the jobspec JSON shape stored in scheduled_jobs.job_spec.
// Defined here to avoid an import cycle with internal/api.
type runnerSpec struct {
	Name     string            `json:"name"`
	Priority string            `json:"priority"`
	Labels   map[string]string `json:"labels"`
	Tasks    []runnerTaskSpec  `json:"tasks"`
}

type runnerTaskSpec struct {
	Name           string            `json:"name"`
	Command        []string          `json:"command,omitempty"`
	Commands       [][]string        `json:"commands,omitempty"`
	Env            map[string]string `json:"env"`
	Requires       map[string]string `json:"requires"`
	TimeoutSeconds *int32            `json:"timeout_seconds"`
	Retries        int32             `json:"retries"`
	DependsOn      []string          `json:"depends_on"`
}

func (r *Runner) fireOne(ctx context.Context, q *store.Queries, row store.ScheduledJob) {
	var spec runnerSpec
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

	jobID, err := r.createJob(ctx, q, spec, row.OwnerID, row.ID)
	if err != nil {
		log.Printf("schedrunner: createJob failed for %s: %v", row.Name, err)
		r.advance(ctx, q, row, pgtype.UUID{}, nextFire)
		return
	}
	r.advance(ctx, q, row, jobID, nextFire)
}

// createJob inserts a job, its tasks, and dependencies using the provided
// (transactional) Queries. Returns the new job's ID.
func (r *Runner) createJob(ctx context.Context, q *store.Queries, spec runnerSpec, ownerID, schedID pgtype.UUID) (pgtype.UUID, error) {
	priority := spec.Priority
	if priority == "" {
		priority = "normal"
	}
	labelsJSON, err := json.Marshal(spec.Labels)
	if err != nil {
		return pgtype.UUID{}, err
	}

	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name:           spec.Name,
		Priority:       priority,
		SubmittedBy:    ownerID,
		Labels:         labelsJSON,
		ScheduledJobID: schedID,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}

	nameToID := make(map[string]pgtype.UUID, len(spec.Tasks))
	for _, ts := range spec.Tasks {
		envJSON, _ := json.Marshal(ts.Env)
		requiresJSON, _ := json.Marshal(ts.Requires)
		// Normalize legacy command: a single argv becomes a one-element commands.
		// Stored job_spec rows may predate the multi-command field; the API
		// validator handles this on submission, so we mirror it here for
		// schedules created before the migration.
		commands := ts.Commands
		if len(commands) == 0 && len(ts.Command) > 0 {
			commands = [][]string{ts.Command}
		}
		commandsJSON, _ := json.Marshal(commands)
		task, err := q.CreateTask(ctx, store.CreateTaskParams{
			JobID:          job.ID,
			Name:           ts.Name,
			Commands:       commandsJSON,
			Env:            envJSON,
			Requires:       requiresJSON,
			TimeoutSeconds: ts.TimeoutSeconds,
			Retries:        ts.Retries,
		})
		if err != nil {
			return pgtype.UUID{}, err
		}
		nameToID[ts.Name] = task.ID
	}

	for _, ts := range spec.Tasks {
		for _, dep := range ts.DependsOn {
			if err := q.CreateTaskDependency(ctx, store.CreateTaskDependencyParams{
				TaskID:          nameToID[ts.Name],
				DependsOnTaskID: nameToID[dep],
			}); err != nil {
				return pgtype.UUID{}, err
			}
		}
	}

	_ = q.NotifyTaskSubmitted(ctx)
	return job.ID, nil
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
