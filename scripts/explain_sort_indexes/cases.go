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
