package schedrunner_test

import (
	"reflect"
	"strings"
	"testing"

	"relay/internal/store"
)

// scheduledJobFields is store.ScheduledJob's COMPLETE field set as of the
// retry-bounds change. Adding a row here is the deliberate act the test below
// exists to force.
var scheduledJobFields = []string{
	"ID", "Name", "OwnerID", "CronExpr", "Timezone", "JobSpec", "OverlapPolicy",
	"Enabled", "NextRunAt", "LastRunAt", "LastJobID", "CreatedAt", "UpdatedAt",
}

// TestScheduledJobRowStillCarriesNoFailureSurface is the TRIPWIRE for
// docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md.
//
// Every user-visible read of a schedule is built from this row - GET
// /v1/scheduled-jobs/{id}, `relay schedules`, the SPA - so asserting that the
// row has no field capable of carrying a fire-time failure is what turns
// "nothing user-visible records it" from prose into a checked claim. When the
// sibling item ships its column and models.go is regenerated, this goes RED.
// That is the point. Go and INVERT the hazard test named below; do not exempt
// the new field here.
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

	if len(got) == len(scheduledJobFields) {
		same := true
		for i := range got {
			if got[i] != scheduledJobFields[i] {
				same = false
				break
			}
		}
		if same {
			return
		}
	}

	// Name the difference in both directions. A REMOVED field matters too: it
	// means a read the hazard test relies on has gone away.
	inWant := make(map[string]bool, len(scheduledJobFields))
	for _, n := range scheduledJobFields {
		inWant[n] = true
	}
	inGot := make(map[string]bool, len(got))
	for _, n := range got {
		inGot[n] = true
	}
	var added, removed []string
	for _, n := range got {
		if !inWant[n] {
			added = append(added, n)
		}
	}
	for _, n := range scheduledJobFields {
		if !inGot[n] {
			removed = append(removed, n)
		}
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
		"If this is bug-2026-08-23-unfireable-schedule-is-invisible shipping its column, the "+
		"hazard test in internal/schedrunner/stored_spec_bounds_test.go must INVERT - see its "+
		"header comment - and this list must be updated in the same commit. The substring hint "+
		"above is colour only: this test gates on the SET, so a column called anything at all "+
		"lands here.", added, removed, got, hint)
}
