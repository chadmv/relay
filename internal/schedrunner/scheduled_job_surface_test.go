package schedrunner_test

import (
	"reflect"
	"strings"
	"testing"

	"relay/internal/store"
)

// scheduledJobFields is store.ScheduledJob's COMPLETE field set as of the
// last_error change. Adding a row here is the deliberate act the test below
// exists to force.
var scheduledJobFields = []string{
	"ID", "Name", "OwnerID", "CronExpr", "Timezone", "JobSpec", "OverlapPolicy",
	"Enabled", "NextRunAt", "LastRunAt", "LastJobID", "CreatedAt", "UpdatedAt",
	"LastError", "LastErrorAt",
}

// TestScheduledJobRowStillCarriesNoFailureSurface is the field-set guard over
// store.ScheduledJob.
//
// IT WAS FILED AS A TRIPWIRE FOR
// docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md, whose whole
// claim was that no field on this row could carry a fire-time failure. That item
// shipped on 2026-08-28: last_error and last_error_at are in the list above, and
// the hazard test in internal/schedrunner/stored_spec_bounds_test.go had its
// hazard framing removed in the same commit rather than being exempted, exactly
// as this header instructed.
//
// THE NAME IS NOW WRONG AND IS KEPT ON PURPOSE. Renaming it would break the
// only link between this guard and the item that explains why an exact-set
// assertion over a generated struct is worth its maintenance cost. What the
// guard buys from here on is unchanged: every user-visible read of a schedule -
// GET /v1/scheduled-jobs/{id}, `relay schedules`, the SPA - is built from this
// row, so a column added or removed without a deliberate act lands here first.
//
// IT ASSERTS THE WHOLE SET, NOT A DENY-LIST, AND THAT IS THE WHOLE DESIGN. The
// first version of this check asserted only that no field name contained
// "error" or "fail", which LastFireStatus, UnfireableSince, LastSkipReason and
// LastRunOutcome all walk straight past - it was calibrated to one proposed
// spelling in a backlog item, and a backlog proposal is not a contract.
// CLAUDE.md's own rule is that deny-lists fail open on the next addition. The
// substring check survives only as failure-message colour, never as the gate.
//
// IT IS ALSO DELIBERATELY UNTAGGED, so it runs in the plain `go test ./...`
// gate. It is pure reflection over a compiled struct and needs no database; the
// hazard test it guards needs Postgres and is integration-tagged, and leaving
// this loop in there meant the sibling's column would redden only the Docker
// lane. Same placement decision as internal/store/createtask_guard_test.go.
func TestScheduledJobRowStillCarriesNoFailureSurface(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeOf(store.ScheduledJob{}))
	got := make([]string, 0, len(fields))
	for _, f := range fields {
		got = append(got, f.Name)
	}

	ok, added, removed := scheduledJobFieldSetDiff(got, scheduledJobFields)
	if ok {
		return
	}

	var hint string
	for _, n := range added {
		l := strings.ToLower(n)
		if strings.Contains(l, "error") || strings.Contains(l, "fail") ||
			strings.Contains(l, "reason") || strings.Contains(l, "status") ||
			strings.Contains(l, "outcome") {
			hint = " " + n + " reads like a failure surface, which is precisely the sibling item's shape."
			break
		}
	}

	t.Fatalf("store.ScheduledJob's field set moved: added %v, removed %v (have %v).%s\n"+
		"That item already shipped (2026-08-28), so a NEW addition here is a NEW "+
		"deliberate act: update this list in the same commit that adds the column, and "+
		"check whether internal/schedrunner/stored_spec_bounds_test.go needs to say "+
		"anything about it. The substring hint above is colour only: this test gates on "+
		"the SET, so a column called anything at all lands here.", added, removed, got, hint)
}

// scheduledJobFieldSetDiff reports whether got and want agree, and names the
// difference in both directions. A REMOVED field matters too: it means a read
// the hazard test relies on has gone away.
//
// AGREEMENT IS DECIDED BY THE SET, NOT THE ORDER, and ok is derived from the two
// difference lists rather than computed separately, so the answer and its
// explanation cannot disagree. A positional check made a pure reorder of
// store.ScheduledJob fail with `added [], removed []`: sqlc emits models.go in
// column order, column order is no part of what this file gates on, and a
// failure naming nothing is one nobody can act on. Field names from
// reflect.VisibleFields are unique, so set semantics lose nothing here.
func scheduledJobFieldSetDiff(got, want []string) (ok bool, added, removed []string) {
	inWant := make(map[string]bool, len(want))
	for _, n := range want {
		inWant[n] = true
	}
	inGot := make(map[string]bool, len(got))
	for _, n := range got {
		inGot[n] = true
	}
	for _, n := range got {
		if !inWant[n] {
			added = append(added, n)
		}
	}
	for _, n := range want {
		if !inGot[n] {
			removed = append(removed, n)
		}
	}
	return len(added) == 0 && len(removed) == 0, added, removed
}

// TestScheduledJobFieldSetDiff_GatesOnTheSetNotTheOrder is the PERMANENT
// discriminating input for the comparison above.
//
// The header of TestScheduledJobRowStillCarriesNoFailureSurface says it "gates
// on the SET". It did not: the agreement check was positional, so a pure reorder
// of store.ScheduledJob - no field added, none removed - failed with the
// literally unactionable message `added [], removed []`. sqlc emits models.go
// fields in column order, and column order is not part of the property this file
// gates on, so a reorder is not a change anybody can act on.
//
// THE REORDER CASE IS FIRST. A poisoned input placed last cannot distinguish a
// comparison that examined it from one that returned before reaching it.
func TestScheduledJobFieldSetDiff_GatesOnTheSetNotTheOrder(t *testing.T) {
	reordered := []string{
		"Name", "ID", "OwnerID", "CronExpr", "Timezone", "JobSpec", "OverlapPolicy",
		"Enabled", "NextRunAt", "LastRunAt", "LastJobID", "LastError", "LastErrorAt",
		"CreatedAt", "UpdatedAt",
	}
	ok, added, removed := scheduledJobFieldSetDiff(reordered, scheduledJobFields)
	if !ok {
		t.Errorf("a pure reorder was reported as a change: added %v, removed %v. Neither list can "+
			"name what to do about it, which is the whole defect.", added, removed)
	}

	// Both real directions must still be reported, or the fix above would have
	// bought order-insensitivity by making the guard inert.
	ok, added, removed = scheduledJobFieldSetDiff(
		[]string{"ID", "Name", "LastFireStatus"}, []string{"ID", "Name"})
	if ok || len(added) != 1 || added[0] != "LastFireStatus" || len(removed) != 0 {
		t.Errorf("an ADDED field must be reported: ok=%v added=%v removed=%v", ok, added, removed)
	}
	ok, added, removed = scheduledJobFieldSetDiff(
		[]string{"ID"}, []string{"ID", "NextRunAt"})
	if ok || len(removed) != 1 || removed[0] != "NextRunAt" || len(added) != 0 {
		t.Errorf("a REMOVED field must be reported: ok=%v added=%v removed=%v", ok, added, removed)
	}
}
