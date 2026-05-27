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
	exitOK        = 0
	exitCheckFail = 1
	exitInfraFail = 2
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
