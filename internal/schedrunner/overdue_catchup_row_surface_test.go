package schedrunner_test

import (
	"reflect"
	"testing"

	"relay/internal/store"
)

// overdueCatchupPageRowFields is the COMPLETE field set of the row
// ListOverdueScheduledJobsForCatchupPage returns. Adding a name here is the
// deliberate act this guard exists to force.
var overdueCatchupPageRowFields = []string{"ID", "Name", "CronExpr", "Timezone"}

// TestOverdueCatchupPageRowCarriesOnlyTheFourColumnsReconcileReads is the
// field-set guard over the reconcile's narrowed read.
//
// THE COLUMN LIST IS THE BOUND, alongside the page size. job_spec is bounded
// only by maxBodyBytes at 1 MiB and ReconcileOnStartup reads it zero times, so
// dropping it is a larger lever per row than the page size is. Restoring
// SELECT *, or adding one column for a validation step somebody bolts on later,
// brings that term back onto the boot path; without this guard nothing goes red.
//
// IT ASSERTS THE SET, NOT A COUNT. A NumField check is satisfied by any four
// fields; the set names what appeared and what went missing, in both directions.
// It shares scheduledJobFieldSetDiff with the guard over store.ScheduledJob for
// that reason.
//
// IT IS DELIBERATELY UNTAGGED. Pure reflection over a compiled struct, no
// database, so it runs in the plain `go test ./...` gate rather than only in the
// Postgres lane - the same placement decision scheduled_job_surface_test.go
// records for itself.
func TestOverdueCatchupPageRowCarriesOnlyTheFourColumnsReconcileReads(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeOf(store.ListOverdueScheduledJobsForCatchupPageRow{}))
	got := make([]string, 0, len(fields))
	for _, f := range fields {
		got = append(got, f.Name)
	}

	ok, added, removed := scheduledJobFieldSetDiff(got, overdueCatchupPageRowFields)
	if ok {
		return
	}

	t.Fatalf("the reconcile's row struct moved: added %v, removed %v (have %v).\n"+
		"ReconcileOnStartup reads ID (the advance and the page cursor), Name (two log lines) "+
		"and CronExpr/Timezone (ParseSchedule), and nothing else. An ADDED field means a column "+
		"came back into a statement that runs ahead of the HTTP listener - if it is JobSpec, that "+
		"is a 1 MiB per-row term. A REMOVED field means the loop lost a value it reads.",
		added, removed, got)
}
