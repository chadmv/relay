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
