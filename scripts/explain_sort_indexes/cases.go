package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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

// tableSpec pairs each table name with its SortSpec from internal/api.
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
	result := "WHERE " + parts[0]
	for _, p := range parts[1:] {
		result += " AND " + p
	}
	return result
}

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
		where := whereClause(col, "")
		if where != "" {
			c.InitialSQL = fmt.Sprintf(
				"EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) SELECT * FROM %s %s ORDER BY %s LIMIT 50",
				col.From, where, order)
		} else {
			c.InitialSQL = fmt.Sprintf(
				"EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) SELECT * FROM %s ORDER BY %s LIMIT 50",
				col.From, order)
		}

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
		cursorPred := fmt.Sprintf("(%s, id) %s (%s, '%s'::uuid)",
			col.SQLExpr, op, literal, cursorID)
		cursorWhere := whereClause(col, cursorPred)
		c.CursorSQL = fmt.Sprintf(
			"EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) SELECT * FROM %s %s ORDER BY %s LIMIT 50",
			col.From, cursorWhere, order)
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
	// Qualify id when the FROM contains a JOIN to avoid ambiguity.
	idExpr := "id"
	if strings.Contains(col.From, " JOIN ") {
		// col.SQLExpr is "alias.column"; extract the alias.
		if dot := strings.Index(col.SQLExpr, "."); dot >= 0 {
			idExpr = col.SQLExpr[:dot] + ".id"
		}
	}
	query := fmt.Sprintf("SELECT %s, %s::text FROM %s %s ORDER BY %s OFFSET %d LIMIT 1",
		col.SQLExpr, idExpr, col.From, where, order, offset)
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
