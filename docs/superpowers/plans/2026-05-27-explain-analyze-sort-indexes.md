# EXPLAIN ANALYZE Sort-Index Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a one-shot Go script under `scripts/explain_sort_indexes/` that spins up a Postgres testcontainer, seeds realistic data, EXPLAINs every configurable sort path, asserts each plan uses the expected composite index, and writes the output to a committed retro file that closes [bug-2026-05-27-explain-analyze-sort-indexes](../../backlog/bug-2026-05-27-explain-analyze-sort-indexes.md).

**Architecture:** Four-file Go program (`main.go`, `seed.go`, `cases.go`, `explain.go`) in a new `scripts/explain_sort_indexes/` directory. Imports `relay/internal/store` for migrations and `relay/internal/api` for the six `SortSpec` definitions. Uses testcontainers-go and pgx (already in `go.mod`). Not a test, not part of `make build` - run once via `go run ./scripts/explain_sort_indexes` and commit the output as a retro artifact.

**Tech Stack:** Go, pgx v5, testcontainers-go, embedded `golang-migrate`, the existing `relay/internal/store.Migrate` entry point.

**Reference spec:** [docs/superpowers/specs/2026-05-27-explain-analyze-sort-indexes-design.md](../specs/2026-05-27-explain-analyze-sort-indexes-design.md)

---

## File Structure

All work lives under one new directory:

```
scripts/explain_sort_indexes/
├── main.go      - orchestration, flag parsing, container lifecycle, exit codes
├── seed.go      - per-table seeders using pgx.CopyFrom
├── cases.go     - case struct + buildCases driven from SortSpec, cursor midpoints
├── explain.go   - EXPLAIN runner + plan-shape assertion + markdown rendering
└── README.md    - purpose, run command, how to read output
```

Additional files touched:
- `docs/retros/2026-05-27-explain-sort-indexes.md` - committed EXPLAIN output (deliverable)
- `docs/backlog/bug-2026-05-27-explain-analyze-sort-indexes.md` - moved to `docs/backlog/closed/`

No existing source files are modified. The script imports existing exported symbols only.

---

## Schema Facts the Implementation Needs

These were established during brainstorming exploration:

- `jobs.priority` is TEXT (not int). Realistic values: `low`, `normal`, `high`, `critical`.
- `jobs.status` is TEXT. Realistic values: `pending`, `queued`, `running`, `dispatched`, `cancelled`, `done`, `failed`.
- `workers.status` is TEXT. Default `offline`. Realistic values: `online`, `offline`, `busy`.
- `workers.last_seen_at`, `reservations.starts_at`, `reservations.ends_at` are nullable TIMESTAMPTZ.
- `users` queries filter `WHERE archived_at IS NULL` - the EXPLAIN SQL must include this filter to match `ListUsersPage*`.
- `agent_enrollments` queries filter `WHERE consumed_at IS NULL AND expires_at > NOW()` - the EXPLAIN SQL must include both. The created_at index from migration 000011 is partial on `consumed_at IS NULL`.
- `jobs` listing joins `users` on `submitted_by`; EXPLAIN SQL mirrors the join.

Migration 000011 supplies `created_at` indexes (e.g. `idx_jobs_created_id`). Migration 000013 supplies the other 19. Both are exercised.

---

## Task 1: Scaffold directory and stub main.go

**Files:**
- Create: `scripts/explain_sort_indexes/main.go`
- Create: `scripts/explain_sort_indexes/seed.go` (empty package stub)
- Create: `scripts/explain_sort_indexes/cases.go` (empty package stub)
- Create: `scripts/explain_sort_indexes/explain.go` (empty package stub)

- [ ] **Step 1: Create directory and stub files**

Create the directory and four files. `main.go` contains a runnable stub that prints "hello" and exits 0; the other three files contain only `package main`.

`scripts/explain_sort_indexes/main.go`:

```go
// Command explain_sort_indexes verifies that every configurable ?sort=
// path on the paginated list endpoints uses a composite index rather
// than a Seq Scan + Sort node.
//
// It spins up a Postgres 16 testcontainer, applies all migrations, seeds
// each table with realistic data, runs EXPLAIN ANALYZE over every
// (table, sort_key, direction) tuple, and asserts each plan's top-level
// access node is an Index Scan on the expected index.
//
// Run:
//
//	go run ./scripts/explain_sort_indexes -out docs/retros/2026-05-27-explain-sort-indexes.md
//
// Exits 0 if every plan passes, 1 if any plan failed or errored,
// 2 if container start / migration / seed failed.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	out := flag.String("out", "", "output markdown path; empty means stdout")
	flag.Parse()
	_ = out
	fmt.Fprintln(os.Stderr, "explain_sort_indexes: stub")
}
```

`scripts/explain_sort_indexes/seed.go`:

```go
package main
```

`scripts/explain_sort_indexes/cases.go`:

```go
package main
```

`scripts/explain_sort_indexes/explain.go`:

```go
package main
```

- [ ] **Step 2: Confirm it builds and runs**

Run: `go run ./scripts/explain_sort_indexes`
Expected stderr: `explain_sort_indexes: stub`
Expected exit code: 0

- [ ] **Step 3: Commit**

```bash
git add scripts/explain_sort_indexes/
git commit -m "scripts: scaffold explain_sort_indexes one-shot

Empty stub for the EXPLAIN ANALYZE sort-index verification script.
Wires up directory layout (main/seed/cases/explain) so subsequent
tasks can fill each file in isolation.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: Bring up Postgres testcontainer and apply migrations

**Files:**
- Modify: `scripts/explain_sort_indexes/main.go` (replace stub body)

- [ ] **Step 1: Implement container bootstrap in main.go**

Replace the body of `main` with logic that starts a Postgres 16 container, applies all migrations via `store.Migrate`, opens a pgxpool, and exits 0. No seeding yet. The container is terminated on exit.

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"relay/internal/store"
)

const (
	exitOK         = 0
	exitCheckFail  = 1
	exitInfraFail  = 2
)

func main() {
	out := flag.String("out", "", "output markdown path; empty means stdout")
	flag.Parse()
	_ = out

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, terminate, err := startPostgres(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "explain_sort_indexes: bring up Postgres: %v\n", err)
		os.Exit(exitInfraFail)
	}
	defer terminate()
	defer pool.Close()

	fmt.Fprintln(os.Stderr, "explain_sort_indexes: container up, migrations applied")
}

// startPostgres launches a Postgres 16 container, runs every embedded
// migration, and returns a pool plus a terminate func.
func startPostgres(ctx context.Context) (*pgxpool.Pool, func(), error) {
	pg, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("relay_explain"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("run container: %w", err)
	}
	terminate := func() { _ = pg.Terminate(context.Background()) }

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		terminate()
		return nil, nil, fmt.Errorf("connection string: %w", err)
	}

	migrateDSN := "pgx5" + dsn[len("postgres"):]
	if err := store.Migrate(migrateDSN); err != nil {
		terminate()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		terminate()
		return nil, nil, fmt.Errorf("open pool: %w", err)
	}
	return pool, terminate, nil
}
```

- [ ] **Step 2: Run and verify**

Run: `go run ./scripts/explain_sort_indexes`
Expected stderr ends with: `explain_sort_indexes: container up, migrations applied`
Expected exit code: 0

If you see a `migrate` error, double check that `store.Migrate` is the right function name by reading `internal/store/migrate.go`.

- [ ] **Step 3: Commit**

```bash
git add scripts/explain_sort_indexes/main.go
git commit -m "scripts(explain_sort_indexes): bring up postgres + migrate

Container bootstrap mirrors internal/store/testhelper_test.go: postgres:16
testcontainer, store.Migrate over the embedded migrations, pgxpool returned
to caller. No seeding or EXPLAINs yet - subsequent tasks add those.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 3: Seed users (foundation for FK references)

**Files:**
- Modify: `scripts/explain_sort_indexes/seed.go`
- Modify: `scripts/explain_sort_indexes/main.go` (call seedUsers)

Users seed first because `jobs.submitted_by`, `scheduled_jobs.owner_id`, `agent_enrollments.created_by`, and `reservations.user_id` all FK to `users`.

- [ ] **Step 1: Add user seeding to seed.go**

```go
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/rand/v2"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Seed counts. jobs is the highest-volume table per the spec; the others
// are sized small enough to keep the run under ~15 seconds.
const (
	usersN          = 10_000
	jobsN           = 100_000
	workersN        = 10_000
	schedJobsN      = 10_000
	reservationsN   = 10_000
	agentEnrollN    = 10_000
)

// firstUsersForFK is the slice of user IDs (the first 200 of usersN)
// that downstream seeders cycle through for FK columns. The full 10k
// exists so the users sort EXPLAINs run against a non-trivial table.
var firstUsersForFK []string

// seed populates every table with realistic skew. Caller runs ANALYZE
// afterwards. Returns first non-nil error.
func seed(ctx context.Context, pool *pgxpool.Pool) error {
	if err := seedUsers(ctx, pool); err != nil {
		return fmt.Errorf("seed users: %w", err)
	}
	return nil
}

func seedUsers(ctx context.Context, pool *pgxpool.Pool) error {
	rows := make([][]any, 0, usersN)
	names := nameVocabulary()
	for i := 0; i < usersN; i++ {
		token := randHex(8)
		email := fmt.Sprintf("user-%s@example.com", token)
		name := names[rand.IntN(len(names))]
		rows = append(rows, []any{name, email, false /*is_admin*/})
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Conn().CopyFrom(ctx,
		pgx.Identifier{"users"},
		[]string{"name", "email", "is_admin"},
		pgx.CopyFromRows(rows),
	); err != nil {
		return fmt.Errorf("copy users: %w", err)
	}

	// Pull the first 200 user IDs for downstream FK use.
	r, err := conn.Conn().Query(ctx,
		`SELECT id::text FROM users ORDER BY created_at, id LIMIT 200`)
	if err != nil {
		return fmt.Errorf("select FK users: %w", err)
	}
	defer r.Close()
	firstUsersForFK = firstUsersForFK[:0]
	for r.Next() {
		var id string
		if err := r.Scan(&id); err != nil {
			return err
		}
		firstUsersForFK = append(firstUsersForFK, id)
	}
	return r.Err()
}

// nameVocabulary returns ~200 distinct first names. Caller picks
// uniformly; the result has lots of repeats which models real data
// where names are not unique.
func nameVocabulary() []string {
	return []string{
		"Aaron", "Abigail", "Adam", "Adrian", "Aiden", "Alan", "Albert",
		"Alex", "Alice", "Allison", "Amanda", "Amber", "Amelia", "Amy",
		"Andrew", "Angela", "Anna", "Anthony", "Ariana", "Arthur",
		"Ashley", "Ava", "Barbara", "Beatrice", "Ben", "Benjamin",
		"Beverly", "Blake", "Bradley", "Brandon", "Brenda", "Brian",
		"Brittany", "Bruce", "Caleb", "Cameron", "Carl", "Carol",
		"Carolyn", "Catherine", "Charles", "Charlotte", "Cheryl",
		"Chloe", "Christian", "Christina", "Christopher", "Cindy",
		"Claire", "Cody", "Connor", "Craig", "Crystal", "Daniel",
		"David", "Deborah", "Dennis", "Diana", "Diane", "Donald",
		"Donna", "Doris", "Dorothy", "Douglas", "Dustin", "Dylan",
		"Edward", "Eleanor", "Elena", "Elijah", "Elizabeth", "Ella",
		"Ellen", "Emily", "Emma", "Eric", "Erica", "Ethan", "Eugene",
		"Evelyn", "Frances", "Frank", "Gabriel", "Gary", "George",
		"Gerald", "Gloria", "Grace", "Gregory", "Hannah", "Harold",
		"Harper", "Heather", "Helen", "Henry", "Holly", "Howard",
		"Ian", "Isabella", "Isaiah", "Jack", "Jackson", "Jacob",
		"Jacqueline", "James", "Jane", "Janet", "Jason", "Jean",
		"Jeffrey", "Jennifer", "Jeremy", "Jessica", "Joan", "Joe",
		"John", "Jonathan", "Joseph", "Joshua", "Joyce", "Judith",
		"Julia", "Julie", "Justin", "Karen", "Katherine", "Kathleen",
		"Kayla", "Keith", "Kelly", "Kenneth", "Kevin", "Kimberly",
		"Kyle", "Laura", "Lauren", "Lawrence", "Lily", "Linda", "Lisa",
		"Logan", "Louis", "Lucas", "Margaret", "Maria", "Marie",
		"Mark", "Martha", "Mary", "Matthew", "Megan", "Melissa",
		"Mia", "Michael", "Michelle", "Mila", "Nancy", "Natalie",
		"Nathan", "Nicholas", "Noah", "Nora", "Olivia", "Patricia",
		"Patrick", "Paul", "Pauline", "Peter", "Philip", "Rachel",
		"Ralph", "Randy", "Raymond", "Rebecca", "Richard", "Robert",
		"Roger", "Ronald", "Rose", "Roy", "Russell", "Ruth", "Ryan",
		"Samantha", "Samuel", "Sandra", "Sarah", "Scott", "Sean",
		"Sharon", "Shirley", "Sophia", "Stephanie", "Stephen", "Steven",
		"Susan", "Tammy", "Teresa", "Thomas", "Timothy", "Tracy",
		"Tyler", "Victoria", "Vincent", "Virginia", "Walter", "Wayne",
		"William", "Willie", "Wyatt", "Yvonne", "Zachary",
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
```

- [ ] **Step 2: Call seed from main.go**

In `main.go`, after the `container up` log line, add:

```go
	if err := seed(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "explain_sort_indexes: seed: %v\n", err)
		os.Exit(exitInfraFail)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		fmt.Fprintf(os.Stderr, "explain_sort_indexes: count users: %v\n", err)
		os.Exit(exitInfraFail)
	}
	fmt.Fprintf(os.Stderr, "explain_sort_indexes: seeded %d users, %d for FK\n",
		count, len(firstUsersForFK))
```

Remove the earlier `container up, migrations applied` log line so the final stderr line reflects the latest progress.

- [ ] **Step 3: Run and verify**

Run: `go run ./scripts/explain_sort_indexes`
Expected stderr ends with: `explain_sort_indexes: seeded 10000 users, 200 for FK`
Expected exit code: 0
Expected runtime: under 5 seconds.

- [ ] **Step 4: Commit**

```bash
git add scripts/explain_sort_indexes/seed.go scripts/explain_sort_indexes/main.go
git commit -m "scripts(explain_sort_indexes): seed users

10k users seeded via pgx.CopyFrom with names drawn from a ~200-entry
vocabulary (lots of intentional duplicates - matches real data). First
200 user IDs are stashed for downstream FK reuse.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 4: Seed jobs (100k with realistic skew)

**Files:**
- Modify: `scripts/explain_sort_indexes/seed.go`

- [ ] **Step 1: Add seedJobs to seed.go**

Add the helper and wire it from `seed`:

```go
// In the seed function body, after seedUsers:
	if err := seedJobs(ctx, pool); err != nil {
		return fmt.Errorf("seed jobs: %w", err)
	}
```

Then append to `seed.go`:

```go
import "time"

var (
	jobPriorities = []string{"low", "normal", "normal", "normal", "high", "high", "critical"}
	jobStatuses   = []string{
		"pending", "queued", "running", "dispatched",
		"done", "done", "done", "done",
		"failed", "cancelled",
	}
)

func seedJobs(ctx context.Context, pool *pgxpool.Pool) error {
	if len(firstUsersForFK) == 0 {
		return fmt.Errorf("firstUsersForFK empty; seedUsers must run first")
	}
	names := jobNameVocabulary()
	rows := make([][]any, 0, jobsN)
	now := time.Now().UTC()
	for i := 0; i < jobsN; i++ {
		name := names[rand.IntN(len(names))]
		priority := jobPriorities[rand.IntN(len(jobPriorities))]
		status := jobStatuses[rand.IntN(len(jobStatuses))]
		// Spread created_at across the last 90 days; updated_at is
		// created_at + 0..6 hours so the two columns sort differently.
		createdOffset := time.Duration(rand.Int64N(int64(90 * 24 * time.Hour)))
		updatedOffset := time.Duration(rand.Int64N(int64(6 * time.Hour)))
		createdAt := now.Add(-createdOffset)
		updatedAt := createdAt.Add(updatedOffset)
		submittedBy := firstUsersForFK[i%len(firstUsersForFK)]
		rows = append(rows, []any{
			name, priority, status, submittedBy,
			[]byte("{}"), // labels JSONB
			createdAt, updatedAt,
		})
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	_, err = conn.Conn().CopyFrom(ctx,
		pgx.Identifier{"jobs"},
		[]string{"name", "priority", "status", "submitted_by", "labels", "created_at", "updated_at"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copy jobs: %w", err)
	}
	return nil
}

// jobNameVocabulary returns ~5000 distinct job names. With 100k rows,
// each name repeats ~20 times - models realistic batch submission.
func jobNameVocabulary() []string {
	verbs := []string{
		"render", "build", "encode", "validate", "compile", "package",
		"deploy", "analyze", "transcode", "extract", "ingest", "export",
		"sync", "backup", "restore", "lint", "test", "publish",
		"index", "crawl", "scan", "summarize", "report", "audit",
		"migrate", "compute", "train", "evaluate", "tag", "transform",
	}
	subjects := []string{
		"shot", "scene", "asset", "clip", "frame", "audio", "lookdev",
		"layout", "comp", "lighting", "geo", "texture", "rig", "cache",
		"sim", "particles", "fx", "matte", "plate", "review",
	}
	suffixes := []string{
		"alpha", "beta", "v01", "v02", "v03", "v04", "v05", "final",
		"draft", "review",
	}
	out := make([]string, 0, len(verbs)*len(subjects)*len(suffixes))
	for _, v := range verbs {
		for _, s := range subjects {
			for _, x := range suffixes {
				out = append(out, fmt.Sprintf("%s-%s-%s", v, s, x))
			}
		}
	}
	return out
}
```

- [ ] **Step 2: Update main.go log to include job count**

After the seed call, change the verification block to also count jobs:

```go
	var users, jobs int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		fmt.Fprintf(os.Stderr, "explain_sort_indexes: count users: %v\n", err)
		os.Exit(exitInfraFail)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil {
		fmt.Fprintf(os.Stderr, "explain_sort_indexes: count jobs: %v\n", err)
		os.Exit(exitInfraFail)
	}
	fmt.Fprintf(os.Stderr, "explain_sort_indexes: seeded %d users, %d jobs\n",
		users, jobs)
```

- [ ] **Step 3: Run and verify**

Run: `time go run ./scripts/explain_sort_indexes`
Expected stderr ends with: `explain_sort_indexes: seeded 10000 users, 100000 jobs`
Expected exit code: 0
Expected runtime: under 15 seconds total (container + seed).

- [ ] **Step 4: Commit**

```bash
git add scripts/explain_sort_indexes/seed.go scripts/explain_sort_indexes/main.go
git commit -m "scripts(explain_sort_indexes): seed 100k jobs

100k rows with realistic skew: priority/status weighted toward common
values, created_at spread across 90 days, updated_at offset from
created_at so the two columns sort differently, submitted_by cycled
across the first 200 users.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: Seed workers, scheduled_jobs, reservations, agent_enrollments

**Files:**
- Modify: `scripts/explain_sort_indexes/seed.go`

- [ ] **Step 1: Add the four remaining seeders**

Wire them into `seed` in the order workers → scheduled_jobs → reservations → agent_enrollments:

```go
// Inside seed(), after seedJobs:
	if err := seedWorkers(ctx, pool); err != nil {
		return fmt.Errorf("seed workers: %w", err)
	}
	if err := seedScheduledJobs(ctx, pool); err != nil {
		return fmt.Errorf("seed scheduled_jobs: %w", err)
	}
	if err := seedReservations(ctx, pool); err != nil {
		return fmt.Errorf("seed reservations: %w", err)
	}
	if err := seedAgentEnrollments(ctx, pool); err != nil {
		return fmt.Errorf("seed agent_enrollments: %w", err)
	}
```

Append the four helpers:

```go
var workerStatuses = []string{"online", "offline", "offline", "busy"}

func seedWorkers(ctx context.Context, pool *pgxpool.Pool) error {
	names := nameVocabulary()
	rows := make([][]any, 0, workersN)
	now := time.Now().UTC()
	for i := 0; i < workersN; i++ {
		name := names[rand.IntN(len(names))]
		hostname := fmt.Sprintf("worker-%s-%d", randHex(4), i)
		status := workerStatuses[rand.IntN(len(workerStatuses))]
		// 30% NULL to exercise both NULLS LAST and NULLS FIRST indexes.
		var lastSeen any
		if rand.IntN(10) >= 3 {
			lastSeen = now.Add(-time.Duration(rand.Int64N(int64(30 * 24 * time.Hour))))
		}
		createdAt := now.Add(-time.Duration(rand.Int64N(int64(180 * 24 * time.Hour))))
		rows = append(rows, []any{
			name, hostname, 8 /*cpu_cores*/, 32 /*ram_gb*/, 0 /*gpu_count*/,
			"" /*gpu_model*/, "linux", 4 /*max_slots*/,
			[]byte("{}"), // labels JSONB
			status, lastSeen, createdAt,
		})
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Conn().CopyFrom(ctx,
		pgx.Identifier{"workers"},
		[]string{"name", "hostname", "cpu_cores", "ram_gb", "gpu_count",
			"gpu_model", "os", "max_slots", "labels",
			"status", "last_seen_at", "created_at"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copy workers: %w", err)
	}
	return nil
}

func seedScheduledJobs(ctx context.Context, pool *pgxpool.Pool) error {
	if len(firstUsersForFK) == 0 {
		return fmt.Errorf("firstUsersForFK empty")
	}
	names := jobNameVocabulary()
	rows := make([][]any, 0, schedJobsN)
	now := time.Now().UTC()
	jobSpec := []byte(`{"tasks":[]}`)
	for i := 0; i < schedJobsN; i++ {
		name := names[rand.IntN(len(names))] + "-sched"
		owner := firstUsersForFK[i%len(firstUsersForFK)]
		nextRun := now.Add(time.Duration(rand.Int64N(int64(30 * 24 * time.Hour))))
		updated := now.Add(-time.Duration(rand.Int64N(int64(30 * 24 * time.Hour))))
		created := updated.Add(-time.Hour)
		rows = append(rows, []any{
			name, owner, "@hourly", "UTC", jobSpec,
			"skip", true, nextRun, created, updated,
		})
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Conn().CopyFrom(ctx,
		pgx.Identifier{"scheduled_jobs"},
		[]string{"name", "owner_id", "cron_expr", "timezone", "job_spec",
			"overlap_policy", "enabled", "next_run_at", "created_at", "updated_at"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copy scheduled_jobs: %w", err)
	}
	return nil
}

func seedReservations(ctx context.Context, pool *pgxpool.Pool) error {
	if len(firstUsersForFK) == 0 {
		return fmt.Errorf("firstUsersForFK empty")
	}
	names := nameVocabulary()
	rows := make([][]any, 0, reservationsN)
	now := time.Now().UTC()
	for i := 0; i < reservationsN; i++ {
		name := names[rand.IntN(len(names))] + "-resv"
		owner := firstUsersForFK[i%len(firstUsersForFK)]
		var starts, ends any
		if rand.IntN(10) >= 3 {
			starts = now.Add(-time.Duration(rand.Int64N(int64(60 * 24 * time.Hour))))
		}
		if rand.IntN(10) >= 3 {
			ends = now.Add(time.Duration(rand.Int64N(int64(60 * 24 * time.Hour))))
		}
		created := now.Add(-time.Duration(rand.Int64N(int64(90 * 24 * time.Hour))))
		// worker_ids is UUID[] NOT NULL DEFAULT '{}'. pgx's CopyFrom
		// needs a typed empty array; []string{} can mis-encode. If a
		// "cannot encode" error appears here, swap to pgtype.Array[uuid.UUID]
		// or replace this seeder with a parameterized INSERT loop.
		rows = append(rows, []any{
			name,
			[]byte("{}"), // selector JSONB
			[]string{},   // worker_ids UUID[]
			owner, "", starts, ends, created,
		})
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Conn().CopyFrom(ctx,
		pgx.Identifier{"reservations"},
		[]string{"name", "selector", "worker_ids", "user_id",
			"project", "starts_at", "ends_at", "created_at"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copy reservations: %w", err)
	}
	return nil
}

func seedAgentEnrollments(ctx context.Context, pool *pgxpool.Pool) error {
	if len(firstUsersForFK) == 0 {
		return fmt.Errorf("firstUsersForFK empty")
	}
	rows := make([][]any, 0, agentEnrollN)
	now := time.Now().UTC()
	for i := 0; i < agentEnrollN; i++ {
		owner := firstUsersForFK[i%len(firstUsersForFK)]
		// 20% consumed; the active listing filters consumed_at IS NULL.
		var consumedAt any
		if rand.IntN(10) < 2 {
			consumedAt = now.Add(-time.Duration(rand.Int64N(int64(24 * time.Hour))))
		}
		// expires_at spread across +/-7 days. The listing additionally
		// filters expires_at > NOW(); both branches need representation.
		expiresOffset := time.Duration(rand.Int64N(int64(14*24*time.Hour))) - 7*24*time.Hour
		expiresAt := now.Add(expiresOffset)
		createdAt := now.Add(-time.Duration(rand.Int64N(int64(7 * 24 * time.Hour))))
		rows = append(rows, []any{
			randHex(32), nil, owner, createdAt, expiresAt, consumedAt, nil,
		})
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Conn().CopyFrom(ctx,
		pgx.Identifier{"agent_enrollments"},
		[]string{"token_hash", "hostname_hint", "created_by", "created_at",
			"expires_at", "consumed_at", "consumed_by"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copy agent_enrollments: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Update main.go verification to print all counts**

Replace the count block in `main.go`:

```go
	counts := map[string]int{}
	for _, table := range []string{"users", "jobs", "workers",
		"scheduled_jobs", "reservations", "agent_enrollments"} {
		var n int
		if err := pool.QueryRow(ctx,
			fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&n); err != nil {
			fmt.Fprintf(os.Stderr, "explain_sort_indexes: count %s: %v\n", table, err)
			os.Exit(exitInfraFail)
		}
		counts[table] = n
	}
	fmt.Fprintf(os.Stderr,
		"explain_sort_indexes: seeded users=%d jobs=%d workers=%d sched=%d resv=%d enroll=%d\n",
		counts["users"], counts["jobs"], counts["workers"],
		counts["scheduled_jobs"], counts["reservations"], counts["agent_enrollments"])
```

- [ ] **Step 3: Run and verify**

Run: `time go run ./scripts/explain_sort_indexes`
Expected stderr ends with: `explain_sort_indexes: seeded users=10000 jobs=100000 workers=10000 sched=10000 resv=10000 enroll=10000`
Expected exit code: 0

- [ ] **Step 4: Commit**

```bash
git add scripts/explain_sort_indexes/seed.go scripts/explain_sort_indexes/main.go
git commit -m "scripts(explain_sort_indexes): seed remaining tables

Workers (10k, 30% NULL last_seen_at), scheduled_jobs (10k), reservations
(10k, 30% NULL starts/ends), agent_enrollments (10k, 20% consumed,
expires_at spread +/-7 days). Each shape mirrors the realistic skew the
spec calls for.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 6: Run ANALYZE on every table

**Files:**
- Modify: `scripts/explain_sort_indexes/main.go`

Without `ANALYZE`, the planner uses default selectivity estimates and may pick Seq Scan even on a 100k table.

- [ ] **Step 1: Add ANALYZE step in main.go**

After the seed verification block, before the closing `Fprintf`, insert:

```go
	for _, table := range []string{"users", "jobs", "workers",
		"scheduled_jobs", "reservations", "agent_enrollments"} {
		if _, err := pool.Exec(ctx, fmt.Sprintf("ANALYZE %s", table)); err != nil {
			fmt.Fprintf(os.Stderr, "explain_sort_indexes: analyze %s: %v\n", table, err)
			os.Exit(exitInfraFail)
		}
	}
	fmt.Fprintln(os.Stderr, "explain_sort_indexes: ANALYZE complete")
```

- [ ] **Step 2: Run and verify**

Run: `go run ./scripts/explain_sort_indexes`
Expected stderr includes: `explain_sort_indexes: ANALYZE complete`
Expected exit code: 0

- [ ] **Step 3: Commit**

```bash
git add scripts/explain_sort_indexes/main.go
git commit -m "scripts(explain_sort_indexes): ANALYZE every seeded table

Without this the planner uses default selectivity estimates and may
pick Seq Scan even on a 100k table - producing false 'regression'
reports against perfectly fine indexes.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 7: Case enumeration driven from SortSpec

**Files:**
- Modify: `scripts/explain_sort_indexes/cases.go`

- [ ] **Step 1: Define case struct and expected-index map**

```go
package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	"relay/internal/api"
)

// explainCase is one (table, sort_key, direction) tuple plus the two
// SQL strings (initial page + cursor-resume) the script will EXPLAIN.
type explainCase struct {
	Table         string
	SortKey       string
	Direction     string // "asc" | "desc"
	ExpectedIndex string
	InitialSQL    string
	CursorSQL     string
}

// expectedIndexes is the hand-written truth table mapping each
// (table, sort_key, direction) tuple to the index migration 000011 or
// 000013 was meant to create. If a sort key is added to a SortSpec
// without updating this map, buildCases returns an error naming the
// missing entry.
var expectedIndexes = map[string]string{
	// jobs - created_at uses migration 000011 (idx_jobs_created_id).
	"jobs|created_at|desc": "idx_jobs_created_id",
	"jobs|created_at|asc":  "idx_jobs_created_id",
	"jobs|name|desc":       "idx_jobs_name_id",
	"jobs|name|asc":        "idx_jobs_name_id",
	"jobs|priority|desc":   "idx_jobs_priority_id",
	"jobs|priority|asc":    "idx_jobs_priority_id",
	"jobs|status|desc":     "idx_jobs_status_id",
	"jobs|status|asc":      "idx_jobs_status_id",
	"jobs|updated_at|desc": "idx_jobs_updated_id",
	"jobs|updated_at|asc":  "idx_jobs_updated_id",

	"workers|created_at|desc":   "idx_workers_created_id",
	"workers|created_at|asc":    "idx_workers_created_id",
	"workers|name|desc":         "idx_workers_name_id",
	"workers|name|asc":          "idx_workers_name_id",
	"workers|status|desc":       "idx_workers_status_id",
	"workers|status|asc":        "idx_workers_status_id",
	"workers|last_seen_at|desc": "idx_workers_last_seen_desc",
	"workers|last_seen_at|asc":  "idx_workers_last_seen_asc",

	"users|created_at|desc": "idx_users_created_id",
	"users|created_at|asc":  "idx_users_created_id",
	"users|name|desc":       "idx_users_name_id",
	"users|name|asc":        "idx_users_name_id",
	"users|email|desc":      "idx_users_email_id",
	"users|email|asc":       "idx_users_email_id",

	"scheduled_jobs|created_at|desc":  "idx_sched_jobs_created_id",
	"scheduled_jobs|created_at|asc":   "idx_sched_jobs_created_id",
	"scheduled_jobs|name|desc":        "idx_sched_jobs_name_id",
	"scheduled_jobs|name|asc":         "idx_sched_jobs_name_id",
	"scheduled_jobs|next_run_at|desc": "idx_sched_jobs_next_run_id",
	"scheduled_jobs|next_run_at|asc":  "idx_sched_jobs_next_run_id",
	"scheduled_jobs|updated_at|desc":  "idx_sched_jobs_updated_id",
	"scheduled_jobs|updated_at|asc":   "idx_sched_jobs_updated_id",

	"reservations|created_at|desc": "idx_reservations_created_id",
	"reservations|created_at|asc":  "idx_reservations_created_id",
	"reservations|name|desc":       "idx_reservations_name_id",
	"reservations|name|asc":        "idx_reservations_name_id",
	"reservations|starts_at|desc":  "idx_reservations_starts_desc",
	"reservations|starts_at|asc":   "idx_reservations_starts_asc",
	"reservations|ends_at|desc":    "idx_reservations_ends_desc",
	"reservations|ends_at|asc":     "idx_reservations_ends_asc",

	"agent_enrollments|created_at|desc": "idx_agent_enr_created_id",
	"agent_enrollments|created_at|asc":  "idx_agent_enr_created_id",
	"agent_enrollments|expires_at|desc": "idx_agent_enr_expires_id",
	"agent_enrollments|expires_at|asc":  "idx_agent_enr_expires_id",
}

// tableSpecs pairs each table name with its SortSpec from internal/api.
// Listing them explicitly here (rather than reflection over package
// vars) keeps the script straightforward.
type tableSpec struct {
	Table string
	Spec  api.SortSpec
}

func tableSpecs() []tableSpec {
	return []tableSpec{
		{"jobs", api.JobsSortSpec},
		{"workers", api.WorkersSortSpec},
		{"users", api.UsersSortSpec},
		{"scheduled_jobs", api.ScheduledJobsSortSpec},
		{"reservations", api.ReservationsSortSpec},
		{"agent_enrollments", api.AgentEnrollmentsSortSpec},
	}
}

// buildCases enumerates one case per (table, sort_key, direction)
// across every SortSpec, attaches the expected index, and fills in the
// SQL strings. SQL bodies and cursor midpoints are wired up in later
// steps; this step asserts the enumeration matches expectedIndexes.
func buildCases(ctx context.Context, pool *pgxpool.Pool) ([]explainCase, error) {
	var cases []explainCase
	for _, ts := range tableSpecs() {
		// Deterministic key order so the output is stable across runs.
		keys := make([]string, 0, len(ts.Spec.Keys))
		for k := range ts.Spec.Keys {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, dir := range []string{"desc", "asc"} {
				mapKey := fmt.Sprintf("%s|%s|%s", ts.Table, key, dir)
				idx, ok := expectedIndexes[mapKey]
				if !ok {
					return nil, fmt.Errorf(
						"no expected index registered for %s (update expectedIndexes in cases.go)",
						mapKey)
				}
				cases = append(cases, explainCase{
					Table:         ts.Table,
					SortKey:       key,
					Direction:     dir,
					ExpectedIndex: idx,
					// SQL filled in by attachSQL in a later task.
				})
			}
		}
	}
	return cases, nil
}
```

- [ ] **Step 2: Wire a smoke call from main.go**

In `main.go`, after ANALYZE, add:

```go
	cases, err := buildCases(ctx, pool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "explain_sort_indexes: build cases: %v\n", err)
		os.Exit(exitInfraFail)
	}
	fmt.Fprintf(os.Stderr, "explain_sort_indexes: built %d cases\n", len(cases))
```

- [ ] **Step 3: Run and verify**

Run: `go run ./scripts/explain_sort_indexes`
Expected stderr ends with: `explain_sort_indexes: built 44 cases`
(22 sort keys × 2 directions = 44 cases)
Expected exit code: 0

If you see a `no expected index registered for ...` error, a SortSpec has a key that `expectedIndexes` doesn't cover. Either add the entry to the map (if the index legitimately exists) or fix the SortSpec.

- [ ] **Step 4: Commit**

```bash
git add scripts/explain_sort_indexes/cases.go scripts/explain_sort_indexes/main.go
git commit -m "scripts(explain_sort_indexes): enumerate cases from SortSpec

Drives case enumeration from the six SortSpec definitions in
internal/api, multiplied by {asc, desc}. Each (table, sort_key,
direction) is matched against a hand-written expectedIndexes map;
a missing entry fails fast at startup - same drift-protection
pattern as internal/mcp/sort_drift_test.go.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 8: Attach SQL bodies and cursor midpoints to each case

**Files:**
- Modify: `scripts/explain_sort_indexes/cases.go`

This is the most intricate task. Each case needs two SQL strings: an initial-page EXPLAIN and a cursor-resume EXPLAIN. The cursor predicate needs a real midpoint value pulled from the seeded data.

- [ ] **Step 1: Add column metadata and SQL templates**

Add to `cases.go`:

```go
// colMeta describes how each sort column behaves: the SELECT projection
// to use, optional table prefix (for joins), and whether the column is
// nullable. Filter SQL captures partial-index predicates that the
// production query applies (e.g. WHERE archived_at IS NULL on users).
type colMeta struct {
	// SQLExpr is the column reference in ORDER BY and cursor predicates,
	// e.g. "j.name" for jobs or "name" for everything else.
	SQLExpr string
	// From is the FROM clause (and any JOIN). Reflects what the
	// production query does so the planner sees the same shape.
	From string
	// Filter is appended to WHERE in addition to the cursor predicate.
	// Empty for tables with no row-filter on the listing.
	Filter string
	// IsTimestamp is true for timestamptz columns; affects cursor
	// midpoint cast.
	IsTimestamp bool
	// Nullable is true for columns where production indexes use NULLS
	// LAST / NULLS FIRST.
	Nullable bool
}

// columns returns the colMeta for every (table, sort_key) pair the
// script knows how to EXPLAIN. The keys must match expectedIndexes.
func columns() map[string]colMeta {
	return map[string]colMeta{
		// jobs uses the email join.
		"jobs|created_at": {SQLExpr: "j.created_at", From: "jobs j JOIN users u ON u.id = j.submitted_by", IsTimestamp: true},
		"jobs|name":       {SQLExpr: "j.name", From: "jobs j JOIN users u ON u.id = j.submitted_by"},
		"jobs|priority":   {SQLExpr: "j.priority", From: "jobs j JOIN users u ON u.id = j.submitted_by"},
		"jobs|status":     {SQLExpr: "j.status", From: "jobs j JOIN users u ON u.id = j.submitted_by"},
		"jobs|updated_at": {SQLExpr: "j.updated_at", From: "jobs j JOIN users u ON u.id = j.submitted_by", IsTimestamp: true},

		"workers|created_at":   {SQLExpr: "created_at", From: "workers", IsTimestamp: true},
		"workers|name":         {SQLExpr: "name", From: "workers"},
		"workers|status":       {SQLExpr: "status", From: "workers"},
		"workers|last_seen_at": {SQLExpr: "last_seen_at", From: "workers", IsTimestamp: true, Nullable: true},

		// users listing filters archived_at IS NULL.
		"users|created_at": {SQLExpr: "created_at", From: "users", Filter: "archived_at IS NULL", IsTimestamp: true},
		"users|name":       {SQLExpr: "name", From: "users", Filter: "archived_at IS NULL"},
		"users|email":      {SQLExpr: "email", From: "users", Filter: "archived_at IS NULL"},

		"scheduled_jobs|created_at":  {SQLExpr: "created_at", From: "scheduled_jobs", IsTimestamp: true},
		"scheduled_jobs|name":        {SQLExpr: "name", From: "scheduled_jobs"},
		"scheduled_jobs|next_run_at": {SQLExpr: "next_run_at", From: "scheduled_jobs", IsTimestamp: true},
		"scheduled_jobs|updated_at":  {SQLExpr: "updated_at", From: "scheduled_jobs", IsTimestamp: true},

		"reservations|created_at": {SQLExpr: "created_at", From: "reservations", IsTimestamp: true},
		"reservations|name":       {SQLExpr: "name", From: "reservations"},
		"reservations|starts_at":  {SQLExpr: "starts_at", From: "reservations", IsTimestamp: true, Nullable: true},
		"reservations|ends_at":    {SQLExpr: "ends_at", From: "reservations", IsTimestamp: true, Nullable: true},

		// agent_enrollments listing filters consumed_at IS NULL and expires_at > NOW().
		"agent_enrollments|created_at": {SQLExpr: "created_at", From: "agent_enrollments", Filter: "consumed_at IS NULL AND expires_at > NOW()", IsTimestamp: true},
		"agent_enrollments|expires_at": {SQLExpr: "expires_at", From: "agent_enrollments", Filter: "consumed_at IS NULL AND expires_at > NOW()", IsTimestamp: true},
	}
}

// orderClause builds the ORDER BY tail for a column + direction.
// Non-null columns: <col> <dir>, id <dir>.
// Nullable columns: <col> <dir> NULLS LAST|FIRST, id <dir>.
func orderClause(col colMeta, dir string) string {
	upper := "DESC"
	if dir == "asc" {
		upper = "ASC"
	}
	if col.Nullable {
		nullPos := "NULLS LAST"
		if dir == "asc" {
			nullPos = "NULLS FIRST"
		}
		return fmt.Sprintf("%s %s %s, id %s", col.SQLExpr, upper, nullPos, upper)
	}
	return fmt.Sprintf("%s %s, id %s", col.SQLExpr, upper, upper)
}

// whereClause merges col.Filter with an optional cursor predicate.
func whereClause(col colMeta, cursor string) string {
	parts := []string{}
	if col.Filter != "" {
		parts = append(parts, col.Filter)
	}
	if cursor != "" {
		parts = append(parts, cursor)
	}
	if len(parts) == 0 {
		return ""
	}
	return "WHERE " + parts[0] + func() string {
		if len(parts) == 1 {
			return ""
		}
		s := ""
		for _, p := range parts[1:] {
			s += " AND " + p
		}
		return s
	}()
}
```

- [ ] **Step 2: Add attachSQL and cursor midpoint lookup**

Append to `cases.go`:

```go
// attachSQL fills InitialSQL and CursorSQL on each case. The cursor
// midpoint is computed by selecting the (col, id) at OFFSET N/2 of the
// table sorted the same way the production query sorts it, then
// formatting that pair into the cursor-resume EXPLAIN string.
func attachSQL(ctx context.Context, pool *pgxpool.Pool, cases []explainCase) error {
	cols := columns()
	for i := range cases {
		c := &cases[i]
		col, ok := cols[c.Table+"|"+c.SortKey]
		if !ok {
			return fmt.Errorf("no column metadata for %s|%s", c.Table, c.SortKey)
		}
		order := orderClause(col, c.Direction)
		c.InitialSQL = fmt.Sprintf(
			"EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) SELECT * FROM %s %s ORDER BY %s LIMIT 50",
			col.From, whereClause(col, ""), order)

		cursorVal, cursorID, err := pickMidpoint(ctx, pool, col, c.Direction)
		if err != nil {
			return fmt.Errorf("midpoint %s|%s|%s: %w",
				c.Table, c.SortKey, c.Direction, err)
		}
		// Postgres can't parameterise EXPLAIN bodies easily across
		// drivers; inline the literal. The midpoint comes from the
		// trusted seeded DB so injection isn't a concern.
		var literal string
		if col.IsTimestamp {
			ts := cursorVal.(time.Time)
			literal = fmt.Sprintf("TIMESTAMPTZ '%s'", ts.Format("2006-01-02 15:04:05.000000-07"))
		} else {
			s := cursorVal.(string)
			// Escape single quotes - PG doubles them.
			s = strings.ReplaceAll(s, "'", "''")
			literal = "'" + s + "'"
		}
		op := "<"
		if c.Direction == "asc" {
			op = ">"
		}
		c.CursorSQL = fmt.Sprintf(
			"EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) SELECT * FROM %s %s ORDER BY %s LIMIT 50",
			col.From,
			whereClause(col,
				fmt.Sprintf("(%s, id) %s (%s, '%s'::uuid)",
					col.SQLExpr, op, literal, cursorID)),
			order)
	}
	return nil
}

// pickMidpoint runs SELECT <col>, id FROM <table> [WHERE filter AND col IS NOT NULL]
// ORDER BY <col> <dir>, id <dir> OFFSET N/2 LIMIT 1 to produce a real
// (value, id) pair the script can use as a cursor.
func pickMidpoint(ctx context.Context, pool *pgxpool.Pool, col colMeta, dir string) (any, string, error) {
	order := orderClause(col, dir)
	wheres := []string{}
	if col.Filter != "" {
		wheres = append(wheres, col.Filter)
	}
	if col.Nullable {
		wheres = append(wheres, col.SQLExpr+" IS NOT NULL")
	}
	where := ""
	if len(wheres) > 0 {
		where = "WHERE " + wheres[0]
		for _, w := range wheres[1:] {
			where += " AND " + w
		}
	}
	// Bare table name for the COUNT (strip any alias).
	tbl := col.From
	if i := strings.Index(tbl, " "); i >= 0 {
		tbl = tbl[:i]
	}
	var n int
	if err := pool.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s %s", tbl, where)).Scan(&n); err != nil {
		return nil, "", fmt.Errorf("count: %w", err)
	}
	if n < 100 {
		return nil, "", fmt.Errorf("table too sparse (%d rows)", n)
	}
	offset := n / 2
	query := fmt.Sprintf("SELECT %s, id::text FROM %s %s ORDER BY %s OFFSET %d LIMIT 1",
		col.SQLExpr, col.From, where, order, offset)
	row := pool.QueryRow(ctx, query)
	var id string
	if col.IsTimestamp {
		var ts time.Time
		if err := row.Scan(&ts, &id); err != nil {
			return nil, "", fmt.Errorf("scan: %w", err)
		}
		return ts, id, nil
	}
	var s string
	if err := row.Scan(&s, &id); err != nil {
		return nil, "", fmt.Errorf("scan: %w", err)
	}
	return s, id, nil
}
```

Update imports at the top of `cases.go`:

```go
import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"relay/internal/api"
)
```

- [ ] **Step 3: Wire attachSQL from main.go**

In `main.go`, after `buildCases`:

```go
	if err := attachSQL(ctx, pool, cases); err != nil {
		fmt.Fprintf(os.Stderr, "explain_sort_indexes: attach SQL: %v\n", err)
		os.Exit(exitInfraFail)
	}
	// Spot-check one case so a regression in SQL generation is visible.
	fmt.Fprintf(os.Stderr, "explain_sort_indexes: cases[0] InitialSQL = %s\n", cases[0].InitialSQL)
```

- [ ] **Step 4: Run and verify**

Run: `go run ./scripts/explain_sort_indexes`
Expected stderr second-to-last line: a SELECT statement against `agent_enrollments` (alphabetically first table) ordered by `created_at`.

Example expected line shape:
```
explain_sort_indexes: cases[0] InitialSQL = EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) SELECT * FROM agent_enrollments WHERE consumed_at IS NULL AND expires_at > NOW() ORDER BY created_at DESC, id DESC LIMIT 50
```

Expected exit code: 0

If you see an "unbalanced AND" or syntax error in the printed SQL, re-read `whereClause` - the bug is there.

- [ ] **Step 5: Commit**

```bash
git add scripts/explain_sort_indexes/cases.go scripts/explain_sort_indexes/main.go
git commit -m "scripts(explain_sort_indexes): generate per-case SQL

Attaches an InitialSQL (LIMIT 50) and a CursorSQL (tuple-comparison
predicate at the table's midpoint) to every case. Filters mirror
production: users gets archived_at IS NULL, agent_enrollments gets
consumed_at IS NULL AND expires_at > NOW(), jobs joins users for the
email projection.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 9: EXPLAIN runner and plan-shape assertion

**Files:**
- Modify: `scripts/explain_sort_indexes/explain.go`

- [ ] **Step 1: Implement explain.go**

```go
package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// caseResult is the outcome of running both EXPLAINs for one case.
type caseResult struct {
	Case        explainCase
	Status      string // "PASS" | "FAIL" | "ERROR"
	Reason      string // populated when Status != PASS
	InitialPlan string
	CursorPlan  string
}

// runExplain captures the full text of an EXPLAIN result.
func runExplain(ctx context.Context, pool *pgxpool.Pool, sql string) (string, error) {
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String(), rows.Err()
}

// indexScanRE matches the first node line of an Index Scan plan. The
// leading whitespace and arrow are part of the EXPLAIN format; the
// capture group is the index name. Forward, backward, and index-only
// variants all parse.
var indexScanRE = regexp.MustCompile(
	`(?m)^\s*->\s*(?:Index Scan|Index Only Scan|Index Scan Backward) using (\S+)\b`)

// checkPlan inspects an EXPLAIN result and returns PASS / FAIL with a
// reason. The plan PASSes iff the first non-Limit node is an Index
// (Only|Backward) Scan whose index name matches expected.
func checkPlan(plan, expected string) (status, reason string) {
	if strings.Contains(plan, "Seq Scan") {
		return "FAIL", "plan contains Seq Scan"
	}
	if strings.Contains(plan, "->  Sort") {
		return "FAIL", "plan contains an explicit Sort node"
	}
	m := indexScanRE.FindStringSubmatch(plan)
	if m == nil {
		return "FAIL", "no Index Scan node found"
	}
	if m[1] != expected {
		return "FAIL", fmt.Sprintf("used index %q, expected %q", m[1], expected)
	}
	return "PASS", ""
}

// explainCaseRun runs both EXPLAINs for a case and returns the result.
// Any SQL error produces Status="ERROR".
func explainCaseRun(ctx context.Context, pool *pgxpool.Pool, c explainCase) caseResult {
	r := caseResult{Case: c}
	initial, err := runExplain(ctx, pool, c.InitialSQL)
	if err != nil {
		r.Status = "ERROR"
		r.Reason = fmt.Sprintf("initial: %v", err)
		return r
	}
	r.InitialPlan = initial
	cursor, err := runExplain(ctx, pool, c.CursorSQL)
	if err != nil {
		r.Status = "ERROR"
		r.Reason = fmt.Sprintf("cursor: %v", err)
		return r
	}
	r.CursorPlan = cursor

	if s, why := checkPlan(initial, c.ExpectedIndex); s != "PASS" {
		r.Status = "FAIL"
		r.Reason = "initial: " + why
		return r
	}
	if s, why := checkPlan(cursor, c.ExpectedIndex); s != "PASS" {
		r.Status = "FAIL"
		r.Reason = "cursor: " + why
		return r
	}
	r.Status = "PASS"
	return r
}
```

- [ ] **Step 2: Wire it in main.go**

In `main.go`, after the spot-check Fprintf, replace it with the actual run loop:

```go
	results := make([]caseResult, 0, len(cases))
	failCount := 0
	for _, c := range cases {
		r := explainCaseRun(ctx, pool, c)
		results = append(results, r)
		if r.Status != "PASS" {
			failCount++
			fmt.Fprintf(os.Stderr, "explain_sort_indexes: %s | %s | %s -> %s: %s\n",
				c.Table, c.SortKey, c.Direction, r.Status, r.Reason)
		}
	}
	fmt.Fprintf(os.Stderr, "explain_sort_indexes: %d/%d PASS\n",
		len(results)-failCount, len(results))
	if failCount > 0 {
		os.Exit(exitCheckFail)
	}
```

- [ ] **Step 3: Run and verify**

Run: `time go run ./scripts/explain_sort_indexes`
Expected stderr summary line: `explain_sort_indexes: N/44 PASS` where ideally N=44.

If N < 44, the script worked correctly but caught a real regression - inspect the per-case FAIL lines, fix migration 000011 or 000013 as needed, and iterate. Do not paper over a FAIL by relaxing `checkPlan`.

Expected runtime: still well under 30 seconds.

- [ ] **Step 4: Commit**

```bash
git add scripts/explain_sort_indexes/explain.go scripts/explain_sort_indexes/main.go
git commit -m "scripts(explain_sort_indexes): run EXPLAIN and assert plan shape

Each case PASSes iff the first non-Limit node is an Index Scan / Index
Only Scan / Index Scan Backward on the expected index. Seq Scan and
explicit Sort nodes are rejected explicitly; wrong-index detection
guards against the planner picking, say, idx_jobs_created_id for an
ORDER BY priority query because the columns happened to correlate in
the seed.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 10: Render markdown output to -out path

**Files:**
- Modify: `scripts/explain_sort_indexes/explain.go` (add renderMarkdown)
- Modify: `scripts/explain_sort_indexes/main.go` (call renderMarkdown)

- [ ] **Step 1: Add renderMarkdown to explain.go**

Append to `explain.go`:

```go
import (
	"io"
	"time"
)

// renderMarkdown writes the full output document: header, summary
// table, then a section per case with the two plans inside <details>
// tags so the document is skim-friendly.
func renderMarkdown(w io.Writer, results []caseResult, pgVersion string) error {
	fmt.Fprintln(w, "# EXPLAIN ANALYZE sort index verification")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "Postgres: %s\n", pgVersion)

	pass := 0
	for _, r := range results {
		if r.Status == "PASS" {
			pass++
		}
	}
	fmt.Fprintf(w, "Result: %d/%d PASS\n\n", pass, len(results))

	fmt.Fprintln(w, "## Summary")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Table | Sort key | Dir | Index | Status | Notes |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- |")
	for _, r := range results {
		c := r.Case
		notes := ""
		if r.Status != "PASS" {
			notes = r.Reason
		}
		fmt.Fprintf(w, "| %s | %s | %s | `%s` | %s | %s |\n",
			c.Table, c.SortKey, c.Direction, c.ExpectedIndex, r.Status, notes)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "## Plans")
	fmt.Fprintln(w)
	for _, r := range results {
		c := r.Case
		fmt.Fprintf(w, "### %s · %s · %s\n\n", c.Table, c.SortKey, c.Direction)
		fmt.Fprintf(w, "Index: `%s` - %s", c.ExpectedIndex, r.Status)
		if r.Reason != "" {
			fmt.Fprintf(w, " (%s)", r.Reason)
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "<details><summary>Initial page plan</summary>")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "```")
		fmt.Fprint(w, r.InitialPlan)
		fmt.Fprintln(w, "```")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "</details>")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "<details><summary>Cursor-resume plan</summary>")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "```")
		fmt.Fprint(w, r.CursorPlan)
		fmt.Fprintln(w, "```")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "</details>")
		fmt.Fprintln(w)
	}
	return nil
}
```

- [ ] **Step 2: Wire renderMarkdown in main.go**

Replace the run loop's tail (between collecting results and the final exit decision) with:

```go
	// Query the running Postgres version for the doc header.
	var pgVersion string
	if err := pool.QueryRow(ctx, "SHOW server_version").Scan(&pgVersion); err != nil {
		pgVersion = "unknown"
	}

	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "explain_sort_indexes: create %s: %v\n", *out, err)
			os.Exit(exitInfraFail)
		}
		defer f.Close()
		w = f
	}
	if err := renderMarkdown(w, results, pgVersion); err != nil {
		fmt.Fprintf(os.Stderr, "explain_sort_indexes: render: %v\n", err)
		os.Exit(exitInfraFail)
	}
```

Add `"io"` to `main.go`'s imports.

- [ ] **Step 3: Run with -out and inspect**

Run:
```bash
go run ./scripts/explain_sort_indexes -out /tmp/explain_sort_indexes.md
```

Expected exit code: 0 (assuming all 44 cases pass).
Expected stderr summary: `explain_sort_indexes: 44/44 PASS`.

Inspect `/tmp/explain_sort_indexes.md`:
- Header has `Result: 44/44 PASS`
- Summary table has 44 rows, all "PASS"
- Each `### <table> · <key> · <dir>` section has two `<details>` blocks
- Each plan starts with `Limit  (cost=...)` and the next line includes `Index Scan ... using idx_*`

- [ ] **Step 4: Commit**

```bash
git add scripts/explain_sort_indexes/explain.go scripts/explain_sort_indexes/main.go
git commit -m "scripts(explain_sort_indexes): render markdown output

Writes a self-contained doc: header with pass/fail summary, sortable
table of every case, then a section per case with the full Initial /
Cursor plans collapsed inside <details> tags so reviewers can skim
the table first and drill in selectively.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 11: Add scripts README

**Files:**
- Create: `scripts/explain_sort_indexes/README.md`

- [ ] **Step 1: Write the README**

`scripts/explain_sort_indexes/README.md`:

```markdown
# explain_sort_indexes

One-shot Go program that verifies every configurable `?sort=` path on
the paginated list endpoints uses a composite index (not a Seq Scan +
Sort node). Closes the EXPLAIN ANALYZE step from the list-endpoint-sort
design that was skipped during implementation.

## What it does

1. Spins up a Postgres 16 testcontainer.
2. Runs every embedded migration (including the index-bearing
   migrations 000011 and 000013).
3. Seeds users (10k), jobs (100k), workers / scheduled_jobs /
   reservations / agent_enrollments (10k each) with realistic skew.
4. Runs `ANALYZE` on every table.
5. For each (table, sort_key, direction) tuple in the six SortSpec
   allowlists, runs `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)` over an
   initial-page query and a cursor-resumption query.
6. Asserts each plan's first non-Limit node is an Index Scan / Index
   Only Scan / Index Scan Backward on the expected index.
7. Writes a markdown report with pass/fail summary + every plan.

## Run

```bash
go run ./scripts/explain_sort_indexes -out docs/retros/2026-05-27-explain-sort-indexes.md
```

Requires Docker. Takes ~20-30 seconds.

## Exit codes

- 0 - every plan passed
- 1 - one or more cases FAIL (wrong index, Seq Scan, etc.) or ERROR
- 2 - container start, migration, or seed failed

## When to re-run

Re-run after:
- Adding a new sort key to any `SortSpec` in `internal/api/` (update
  `expectedIndexes` in `cases.go` to point at the new index, then re-run).
- Modifying migration 000011 or 000013.
- Changing the per-endpoint list query's WHERE clause - that may break
  the filters in `columns()` in `cases.go`.

## Reading the output

The summary table at the top shows pass/fail status for all 44 cases.
For any FAIL, drill into the `### <table> · <key> · <dir>` section to
see the actual plan. Typical failure modes:

- "plan contains Seq Scan" - the index doesn't exist or the planner
  decided it was cheaper to seq-scan. Check that the index migration
  ran and that the seed is large enough.
- "used index X, expected Y" - the planner picked a different index
  than the one migration 000013 was meant to create. Usually means
  another index incidentally covers the predicate; the named index
  may be redundant.
```

- [ ] **Step 2: Commit**

```bash
git add scripts/explain_sort_indexes/README.md
git commit -m "scripts(explain_sort_indexes): add README

Short doc explaining what the script does, how to run it, what the
exit codes mean, and how to interpret a failing case. Co-located so a
maintainer who finds the directory can self-serve.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 12: Run end-to-end, capture retro, close backlog

**Files:**
- Create: `docs/retros/2026-05-27-explain-sort-indexes.md` (generated)
- Move: `docs/backlog/bug-2026-05-27-explain-analyze-sort-indexes.md` → `docs/backlog/closed/`

- [ ] **Step 1: Run and capture the retro**

```bash
go run ./scripts/explain_sort_indexes -out docs/retros/2026-05-27-explain-sort-indexes.md
echo "Exit: $?"
```

Expected exit code: 0 (assuming the indexes are wired correctly).

If exit is 1, inspect the FAIL rows in the generated doc. There are two outcomes:

1. **Real regression.** The index is missing, named wrong, or the planner won't use it because of the query shape. Fix migration `000013` (or `000011`), regenerate the store layer if needed (`make generate`), re-seed, re-run. Iterate until exit 0.

2. **False positive.** The planner picked a different but legitimately-equivalent index. Audit the case - update `expectedIndexes` in `cases.go` only if the alternative is genuinely correct; otherwise treat as case 1.

Document any iteration loop here in the retro itself (append a "Findings" section between the auto-generated header and the summary table) so future readers know what surfaced.

- [ ] **Step 2: Skim the generated doc**

Open `docs/retros/2026-05-27-explain-sort-indexes.md` and verify:
- Result line reads `44/44 PASS`.
- Every row in the summary table shows `PASS`.
- A few spot-checks of `### <table>` sections show plans starting with `Limit ...` then `Index Scan using <expected>`.

- [ ] **Step 3: Move backlog item to closed**

```bash
mkdir -p docs/backlog/closed
git mv docs/backlog/bug-2026-05-27-explain-analyze-sort-indexes.md docs/backlog/closed/
```

- [ ] **Step 4: Commit retro + backlog move together**

```bash
git add docs/retros/2026-05-27-explain-sort-indexes.md docs/backlog/closed/bug-2026-05-27-explain-analyze-sort-indexes.md
git commit -m "retro: EXPLAIN ANALYZE sort index verification (44/44 PASS)

Captured EXPLAIN output from a clean run of scripts/explain_sort_indexes
against a 100k-row jobs seed (plus 10k each for the five other tables).
Every (table, sort_key, direction) tuple uses the expected composite
index from migration 000011 or 000013.

Closes the verification step from the list-endpoint-sort design.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

- [ ] **Step 5: Final verification**

Run: `go run ./scripts/explain_sort_indexes` (no `-out`, so output goes to stdout)
Confirm: exit code 0, stderr summary `explain_sort_indexes: 44/44 PASS`.

Run: `git log --oneline -15`
Confirm: every task's commit is present, none accidentally squashed.

The backlog item is closed, the retro is committed, and the script can be re-run any time the indexes or SortSpecs change.

---

## Notes on the plan

- **No unit tests for the script.** This is a one-shot diagnostic tool per the spec's "What this is NOT" section. Each task's verification step (running the script and observing the output) is the test.
- **`go run` works without `go build`.** No need to add this to `make build`.
- **No new dependencies.** `testcontainers-go`, `pgx/v5`, `pgxpool`, and `golang-migrate` are all already in `go.mod` (used by the existing integration tests).
- **Em dashes.** Per CLAUDE.md, regular hyphens only in any new prose. The plan body and the script's docstrings all use hyphens.
- **Determinism.** `cases.go` sorts sort-key names alphabetically so output is stable across runs. Seed randomness uses `math/rand/v2`'s package-level RNG; seeding it would be unnecessary speculation (the spec doesn't ask for reproducibility, and EXPLAIN plan shape is robust to the seed's exact distribution).
