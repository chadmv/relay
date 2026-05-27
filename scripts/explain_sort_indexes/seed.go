package main

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Seed counts. jobs is the highest-volume table per the spec; the others
// are sized small enough to keep the run under ~15 seconds.
const (
	usersN        = 10_000
	jobsN         = 100_000
	workersN      = 10_000
	schedJobsN    = 10_000
	reservationsN = 10_000
	agentEnrollN  = 10_000
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
	if err := seedJobs(ctx, pool); err != nil {
		return fmt.Errorf("seed jobs: %w", err)
	}
	return nil
}

func seedUsers(ctx context.Context, pool *pgxpool.Pool) error {
	// Dummy bcrypt hash (cost 4, plaintext "x") used for all seed users.
	// Real auth is not exercised here; we just need a non-empty value.
	const dummyHash = "$2a$04$5twkSN2CvXUGYAJb9YRcguRCDqMPgVGMnVbm5OhRQFMOAFlLbVzKW"

	rows := make([][]any, 0, usersN)
	names := nameVocabulary()
	for i := 0; i < usersN; i++ {
		token := randHex(8)
		email := fmt.Sprintf("user-%s@example.com", token)
		name := names[rand.IntN(len(names))]
		rows = append(rows, []any{name, email, dummyHash, false /*is_admin*/})
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Conn().CopyFrom(ctx,
		pgx.Identifier{"users"},
		[]string{"name", "email", "password_hash", "is_admin"},
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
	if _, err := crand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

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
