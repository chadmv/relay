//go:build integration

package store_test

import (
	"context"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// indexPredicateLiteralRe pulls the single-quoted literals out of a pg_get_expr
// rendering of a partial index's WHERE clause. Postgres renders an IN list as
// `((status)::text = ANY ((ARRAY['dispatched'::character varying, ...])::text[]))`,
// so matching the quoted values is stable against the casts and the ANY/ARRAY
// spelling in a way that string-comparing the whole expression is not.
var indexPredicateLiteralRe = regexp.MustCompile(`'([^']*)'`)

// TestActiveTaskIndexPredicateMatchesTheAssignmentPartition reads
// idx_tasks_worker_active's WHERE clause back off a live database and requires it
// to name exactly the statuses the assignment-partition statements admit.
//
// The consequence of drift between the two is not a wrong answer, it is a silent
// plan change: Postgres uses a partial index only where the query predicate
// IMPLIES the index predicate, so a statement admitting a status this predicate
// omits is served by a sequential scan over every task row the system has ever
// created - no error, no log line. CountActiveTasksByAllWorkers is the worst
// case, because the dispatcher runs it every cycle.
//
// A predicate is asserted rather than an EXPLAIN plan, deliberately: plan choice
// depends on statistics and table size, so a green EXPLAIN on a small test table
// proves nothing and a red one is a flake. The predicate is the property; the
// plan is its consequence.
func TestActiveTaskIndexPredicateMatchesTheAssignmentPartition(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var pred string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT pg_get_expr(i.indpred, i.indrelid)
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = 'idx_tasks_worker_active'`,
	).Scan(&pred), "idx_tasks_worker_active must exist and must still be a PARTIAL index; "+
		"a NULL indpred means it became a full index and every statement's plan changed")

	var got []string
	for _, m := range indexPredicateLiteralRe.FindAllStringSubmatch(pred, -1) {
		got = append(got, m[1])
	}
	sort.Strings(got)

	require.Equal(t, []string{"dispatched", "preparing", "running"}, got,
		"idx_tasks_worker_active's predicate is %q. It must name exactly the statuses the "+
			"assignment-partition statements admit. A status in the statements and not here does "+
			"not make any of them WRONG; it makes all of them scan the whole table", pred)
}
