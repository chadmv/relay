// Command qcost measures what a ?q= text search on GET /v1/jobs costs at the
// database, and records the INPUT with every number.
//
// It exists because the two numbers this area inherits - "about 283 ms" for a
// no-match needle at 200k rows and "about 31 ms" for a plan node's share at 50k
// rows - record neither the needle nor which statement was timed, and a
// measurement without its input reads as the typical case.
//
// It times the PRODUCTION statements through internal/store, not hand-written
// SQL, and reports the COUNT statement and the LIST statement separately,
// because the inherited 283 ms does not say which it was.
//
// Run:
//
//	go run ./scripts/qcost -out docs/retros/2026-09-04-q-cost-measurement.md
//
// Exits 0 on success, 2 if the container, migrations or seeding failed.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const exitInfraFail = 2

// pgImage is the same image scripts/explain_sort_indexes and both integration
// lanes use. The Postgres version is one of the inputs a number must carry, so
// it is spelled once here and rendered into the report.
const pgImage = "postgres:16"

// nameVocabulary sizes the match rate: job names cycle a vocabulary of this
// many words, so a needle matching one whole word matches about 1/N of rows.
const nameVocabulary = 100

func main() {
	usersN := flag.Int("users", 10_000, "rows to seed into users")
	jobsN := flag.Int("jobs", 200_000, "rows to seed into jobs")
	repeat := flag.Int("repeat", 20, "timed executions per statement")
	limit := flag.Int("limit", 50, "page limit passed to the list statement")
	out := flag.String("out", "", "output markdown path; empty means stdout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, version, terminate, err := startPostgres(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qcost: bring up Postgres: %v\n", err)
		os.Exit(exitInfraFail)
	}
	defer terminate()

	fmt.Fprintf(os.Stderr, "qcost: seeding %d users and %d jobs...\n", *usersN, *jobsN)
	seedStart := time.Now()
	if err := seed(ctx, pool, *usersN, *jobsN); err != nil {
		fmt.Fprintf(os.Stderr, "qcost: seed: %v\n", err)
		os.Exit(exitInfraFail)
	}
	if _, err := pool.Exec(ctx, "ANALYZE users, jobs"); err != nil {
		fmt.Fprintf(os.Stderr, "qcost: analyze: %v\n", err)
		os.Exit(exitInfraFail)
	}
	fmt.Fprintf(os.Stderr, "qcost: seeded in %s\n", time.Since(seedStart).Round(time.Second))

	rep, err := run(ctx, pool, runOpts{
		usersN: *usersN, jobsN: *jobsN, repeat: *repeat, limit: *limit, pgVersion: version,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "qcost: measure: %v\n", err)
		os.Exit(exitInfraFail)
	}

	w := io.Writer(os.Stdout)
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "qcost: create %s: %v\n", *out, err)
			os.Exit(exitInfraFail)
		}
		defer f.Close()
		w = f
	}
	if _, err := io.WriteString(w, rep); err != nil {
		fmt.Fprintf(os.Stderr, "qcost: write report: %v\n", err)
		os.Exit(exitInfraFail)
	}
}

func startPostgres(ctx context.Context) (*pgxpool.Pool, string, func(), error) {
	pg, err := tcpostgres.Run(ctx, pgImage,
		tcpostgres.WithDatabase("relay_qcost"),
		tcpostgres.WithUsername("relay"),
		tcpostgres.WithPassword("relay"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		return nil, "", nil, fmt.Errorf("run container: %w", err)
	}
	terminate := func() { _ = pg.Terminate(context.Background()) }

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		terminate()
		return nil, "", nil, fmt.Errorf("connection string: %w", err)
	}
	if err := store.Migrate("pgx5" + dsn[len("postgres"):]); err != nil {
		terminate()
		return nil, "", nil, fmt.Errorf("migrate: %w", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		terminate()
		return nil, "", nil, fmt.Errorf("open pool: %w", err)
	}
	var version string
	if err := pool.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		terminate()
		return nil, "", nil, fmt.Errorf("read version: %w", err)
	}
	return pool, version, terminate, nil
}

// seed writes usersN users and jobsN jobs by COPY. Job names cycle a vocabulary
// of nameVocabulary words, so a needle equal to one word matches about
// jobsN/nameVocabulary rows - which is what makes the match rate an INPUT this
// program can state rather than a property of a random draw.
func seed(ctx context.Context, pool *pgxpool.Pool, usersN, jobsN int) error {
	// Dummy bcrypt hash; real auth is not exercised here.
	const dummyHash = "$2a$04$5twkSN2CvXUGYAJb9YRcguRCDqMPgVGMnVbm5OhRQFMOAFlLbVzKW"

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	userRows := make([][]any, 0, usersN)
	for i := 0; i < usersN; i++ {
		userRows = append(userRows, []any{
			fmt.Sprintf("user %d", i),
			fmt.Sprintf("user-%d@relay.test", i),
			false,
			dummyHash,
		})
	}
	if _, err := conn.Conn().CopyFrom(ctx, pgx.Identifier{"users"},
		[]string{"name", "email", "is_admin", "password_hash"},
		pgx.CopyFromRows(userRows)); err != nil {
		return fmt.Errorf("copy users: %w", err)
	}

	var ownerIDs []pgtype.UUID
	rows, err := conn.Query(ctx, "SELECT id FROM users ORDER BY email LIMIT 200")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ownerIDs = append(ownerIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ownerIDs) == 0 {
		return fmt.Errorf("no users to own jobs")
	}

	now := time.Now().UTC()
	jobRows := make([][]any, 0, jobsN)
	for i := 0; i < jobsN; i++ {
		createdAt := now.Add(-time.Duration(i) * time.Second)
		jobRows = append(jobRows, []any{
			fmt.Sprintf("job-%d-%s", i, vocabWord(i%nameVocabulary)),
			"normal",
			"pending",
			ownerIDs[i%len(ownerIDs)],
			[]byte("{}"),
			createdAt,
			createdAt,
		})
	}
	if _, err := conn.Conn().CopyFrom(ctx, pgx.Identifier{"jobs"},
		[]string{"name", "priority", "status", "submitted_by", "labels", "created_at", "updated_at"},
		pgx.CopyFromRows(jobRows)); err != nil {
		return fmt.Errorf("copy jobs: %w", err)
	}
	return nil
}

// vocabWord renders the nth vocabulary word. Deliberately synthetic and
// deliberately long enough that no word is a substring of another, so a needle
// equal to one word matches exactly the rows carrying it.
func vocabWord(n int) string { return fmt.Sprintf("shotword%03d", n) }

type runOpts struct {
	usersN, jobsN, repeat, limit int
	pgVersion                    string
}

// measurement is one timed statement with everything a reader needs to know
// what produced the number.
type measurement struct {
	caseName  string
	statement string
	needle    string
	matched   int64
	min       time.Duration
	median    time.Duration
	max       time.Duration
}

func run(ctx context.Context, pool *pgxpool.Pool, o runOpts) (string, error) {
	q := store.New(pool)

	// The exact needles, spelled once and rendered into the report, because the
	// needle is the input every inherited measurement is missing.
	const noMatchNeedle = "zqxjvk-matches-nothing"
	matchNeedle := vocabWord(7)

	var out []measurement

	timeIt := func(name, stmt, needle string, matched int64, fn func() error) error {
		durs := make([]time.Duration, 0, o.repeat)
		for i := 0; i < o.repeat; i++ {
			start := time.Now()
			if err := fn(); err != nil {
				return fmt.Errorf("%s/%s: %w", name, stmt, err)
			}
			durs = append(durs, time.Since(start))
		}
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		out = append(out, measurement{
			caseName: name, statement: stmt, needle: needle, matched: matched,
			min: durs[0], median: durs[len(durs)/2], max: durs[len(durs)-1],
		})
		return nil
	}

	countMatches := func(needle string) (int64, error) {
		return q.CountJobsWithText(ctx, store.CountJobsWithTextParams{Q: &needle})
	}

	listParams := func(needle *string) store.ListJobsWithEmailPageParams {
		return store.ListJobsWithEmailPageParams{Q: needle, PageLimit: int32(o.limit)}
	}

	// Case 1: unfiltered. The regression check for "unfiltered polling is
	// unaffected" and for the pool-wide timeout.
	if err := timeIt("unfiltered", "CountJobs", "(none)", int64(o.jobsN), func() error {
		_, err := q.CountJobs(ctx, store.CountJobsParams{})
		return err
	}); err != nil {
		return "", err
	}
	if err := timeIt("unfiltered", "ListJobsWithEmailPage", "(none)", int64(o.jobsN), func() error {
		_, err := q.ListJobsWithEmailPage(ctx, listParams(nil))
		return err
	}); err != nil {
		return "", err
	}

	// Case 2: a needle that matches NOTHING. Expected unchanged by this slice.
	noMatched, err := countMatches(noMatchNeedle)
	if err != nil {
		return "", err
	}
	needle := noMatchNeedle
	if err := timeIt("no-match needle", "CountJobsWithText", noMatchNeedle, noMatched, func() error {
		_, err := q.CountJobsWithText(ctx, store.CountJobsWithTextParams{Q: &needle})
		return err
	}); err != nil {
		return "", err
	}
	if err := timeIt("no-match needle", "ListJobsWithEmailPage", noMatchNeedle, noMatched, func() error {
		_, err := q.ListJobsWithEmailPage(ctx, listParams(&needle))
		return err
	}); err != nil {
		return "", err
	}

	// Case 3: a needle that matches about 1% of rows. Records that the cost is
	// not monotone in match count, which is why the worst case is a needle that
	// matches nothing: a LIMIT can never be satisfied by a predicate nothing
	// satisfies, so the scan runs to completion.
	someMatched, err := countMatches(matchNeedle)
	if err != nil {
		return "", err
	}
	mn := matchNeedle
	if err := timeIt("matching needle", "CountJobsWithText", matchNeedle, someMatched, func() error {
		_, err := q.CountJobsWithText(ctx, store.CountJobsWithTextParams{Q: &mn})
		return err
	}); err != nil {
		return "", err
	}
	if err := timeIt("matching needle", "ListJobsWithEmailPage", matchNeedle, someMatched, func() error {
		_, err := q.ListJobsWithEmailPage(ctx, listParams(&mn))
		return err
	}); err != nil {
		return "", err
	}

	return render(out, o), nil
}

func render(ms []measurement, o runOpts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# `?q=` cost measurement\n\n")
	fmt.Fprintf(&b, "Produced by `go run ./scripts/qcost`. Every row carries its own input; a\n")
	fmt.Fprintf(&b, "measurement without its input reads as the typical case.\n\n")
	fmt.Fprintf(&b, "## Inputs, shared by every row\n\n")
	fmt.Fprintf(&b, "- `jobs` rows: **%d**\n", o.jobsN)
	fmt.Fprintf(&b, "- `users` rows: **%d**\n", o.usersN)
	fmt.Fprintf(&b, "- sort arm: `-created_at` (`ListJobsWithEmailPage`, the default)\n")
	fmt.Fprintf(&b, "- page limit: **%d**\n", o.limit)
	fmt.Fprintf(&b, "- other filters on the request: **none** (no `status`, no `scheduled_job_id`, "+
		"no `mine`, no `since`, no `until`)\n")
	fmt.Fprintf(&b, "- executions per statement: **%d**\n", o.repeat)
	fmt.Fprintf(&b, "- Postgres: `%s`\n", strings.TrimSpace(o.pgVersion))
	fmt.Fprintf(&b, "- statement timing only: this is the database's wall time for the statement, "+
		"NOT a whole HTTP request\n\n")
	fmt.Fprintf(&b, "## Results\n\n")
	fmt.Fprintf(&b, "| case | statement | needle | rows matched | min | median | max |\n")
	fmt.Fprintf(&b, "|---|---|---|---:|---:|---:|---:|\n")
	for _, m := range ms {
		fmt.Fprintf(&b, "| %s | `%s` | `%s` | %d | %s | %s | %s |\n",
			m.caseName, m.statement, m.needle, m.matched,
			ms2(m.min), ms2(m.median), ms2(m.max))
	}
	b.WriteString("\n")
	return b.String()
}

func ms2(d time.Duration) string {
	return fmt.Sprintf("%.2f ms", float64(d.Nanoseconds())/1e6)
}
