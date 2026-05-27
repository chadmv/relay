package main

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"math/rand/v2"

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
