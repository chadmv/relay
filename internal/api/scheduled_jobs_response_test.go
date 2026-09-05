package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is one of the two default-lane siblings covering
// docs/backlog/bug-2026-08-23-unfireable-schedule-is-invisible.md. The
// end-to-end proof, against a real TickOnce and a real HTTP server, is in
// internal/api/scheduled_jobs_failure_visibility_integration_test.go. The
// other sibling is internal/schedrunner/failure_test.go.
//
// WHAT THIS PINS, with no database: the wire contract. Field names,
// absent-not-zero for a healthy schedule, present-with-values for a failing one,
// and the arity relationship between the row and the response.
//
// IT MUST NOT ASSERT THROUGH scheduledJobResponse. A fixture built from the type
// under test agrees with itself by construction on both the key names and the
// omitempty behaviour, and a deep-equal against such a fixture cannot see an
// absent optional field at all. Everything below goes through a
// map[string]any decoded from real marshalled JSON.

func mustResponseMap(t *testing.T, row store.ScheduledJob) map[string]any {
	t.Helper()
	b, err := json.Marshal(toScheduledJobResponse(row))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func cleanScheduledJobRow() store.ScheduledJob {
	now := pgtype.Timestamptz{Time: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC), Valid: true}
	return store.ScheduledJob{
		Name:          "nightly",
		CronExpr:      "@daily",
		Timezone:      "UTC",
		JobSpec:       []byte(`{"name":"n","tasks":[]}`),
		OverlapPolicy: "skip",
		Enabled:       true,
		NextRunAt:     now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// TestToScheduledJobResponse_HealthyScheduleOmitsBothFailureKeys is the absent
// half, and it is the half that matters most: ABSENT MEANS HEALTHY. The keys
// must not be present as "" and must not be present as null, because every
// client - the SPA's `schedule.last_error ? ... : null`, the CLI's
// `*string != nil`, the Python SDK's `Optional[str] = None` - reads presence as
// the signal.
func TestToScheduledJobResponse_HealthyScheduleOmitsBothFailureKeys(t *testing.T) {
	m := mustResponseMap(t, cleanScheduledJobRow())

	for _, k := range []string{"last_error", "last_error_at"} {
		if _, present := m[k]; present {
			t.Errorf("a schedule with no recorded failure must omit %q entirely, got %#v", k, m[k])
		}
	}
	// Control: the keys that are ALWAYS present still are, so a mutation that
	// dropped every optional key would not look like a pass here.
	for _, k := range []string{"id", "name", "cron_expr", "enabled", "next_run_at"} {
		if _, present := m[k]; !present {
			t.Errorf("required key %q is missing from the response", k)
		}
	}
}

// TestToScheduledJobResponse_FailingScheduleCarriesBothFailureKeys is the
// present half, asserted against HAND-WRITTEN key names and values.
func TestToScheduledJobResponse_FailingScheduleCarriesBothFailureKeys(t *testing.T) {
	row := cleanScheduledJobRow()
	text := "task t: retries must be between 0 and 10"
	row.LastError = &text
	row.LastErrorAt = pgtype.Timestamptz{Time: time.Date(2026, 8, 28, 11, 59, 0, 0, time.UTC), Valid: true}

	m := mustResponseMap(t, row)

	got, ok := m["last_error"].(string)
	if !ok {
		t.Fatalf("last_error must be a JSON string, got %T (%#v)", m["last_error"], m["last_error"])
	}
	if got != text {
		t.Errorf("last_error = %q, want %q", got, text)
	}

	at, ok := m["last_error_at"].(string)
	if !ok {
		t.Fatalf("last_error_at must be a JSON string, got %T (%#v)", m["last_error_at"], m["last_error_at"])
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		t.Errorf("last_error_at must be RFC3339, got %q: %v", at, err)
	}
}

// TestToScheduledJobResponse_LastJobStatusIsPairedToLastJobID pins the CLOSED
// KEY SET: last_job_status is present exactly when last_job_id is present. Not
// "usually" - the two keys appear together or neither appears.
//
// Asserted against HAND-WRITTEN key names through a map[string]any decoded from
// real marshalled JSON, for the reason this file's header gives: an absent
// omitempty field compares equal on both sides whatever its name, so a
// deep-equal against a typed fixture could not see the field at all.
func TestToScheduledJobResponse_LastJobStatusIsPairedToLastJobID(t *testing.T) {
	t.Run("no last job: neither key", func(t *testing.T) {
		m := mustResponseMap(t, cleanScheduledJobRow())
		for _, k := range []string{"last_job_id", "last_job_status"} {
			if _, present := m[k]; present {
				t.Errorf("a schedule that has produced no job must omit %q entirely, got %#v", k, m[k])
			}
		}
		// Control: the always-present keys still are, so a mutant that dropped
		// every optional key would not look like a pass here.
		for _, k := range []string{"id", "name", "cron_expr", "enabled", "next_run_at"} {
			if _, present := m[k]; !present {
				t.Errorf("required key %q is missing from the response", k)
			}
		}
	})

	t.Run("last job present: both keys", func(t *testing.T) {
		row := cleanScheduledJobRow()
		row.LastJobID = pgtype.UUID{Valid: true}
		copy(row.LastJobID.Bytes[:], []byte{
			0x3f, 0x0a, 0x1b, 0x2c, 0x4d, 0x5e, 0x4f, 0x60,
			0x81, 0x92, 0xa3, 0xb4, 0xc5, 0xd6, 0xe7, 0xf8})

		resp := toScheduledJobResponse(row)
		// fillLastJobStatuses is what supplies the value in production; the
		// mapper cannot, because no column carries it.
		resp.LastJobStatus = "failed"

		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		for _, k := range []string{"last_job_id", "last_job_status"} {
			if _, present := m[k]; !present {
				t.Errorf("a schedule with a last job must carry %q", k)
			}
		}
		if got, _ := m["last_job_status"].(string); got != "failed" {
			t.Errorf("last_job_status = %#v, want %q", m["last_job_status"], "failed")
		}
	})
}

// TestScheduledJobResponse_ArityMatchesTheRow is the ARITY CHECK for a
// hand-written field-by-field copy.
//
// toScheduledJobResponse maps store.ScheduledJob to scheduledJobResponse by
// hand, one assignment per field. A mapper like that silently drops anything
// added to its source: a new column would land in models.go, in the SQL, in the
// database, and simply never reach a client, with every existing test green.
//
// The relationship is response = row + 2. Neither extra field has a column:
// OwnerEmail comes from fillOwnerEmails and LastJobStatus from
// fillLastJobStatuses, each a separate lookup.
// If this fails after adding a column, add the mapping AND its assertions; if it
// fails after adding a response-only field, update the constant below and say in
// the commit message what supplies it.
func TestScheduledJobResponse_ArityMatchesTheRow(t *testing.T) {
	const responseOnlyFields = 2 // OwnerEmail (fillOwnerEmails), LastJobStatus (fillLastJobStatuses)

	rowFields := reflect.TypeOf(store.ScheduledJob{}).NumField()
	respFields := reflect.TypeOf(scheduledJobResponse{}).NumField()

	if respFields != rowFields+responseOnlyFields {
		t.Fatalf("scheduledJobResponse has %d fields and store.ScheduledJob has %d; expected %d = %d + %d "+
			"response-only field(s). toScheduledJobResponse is a hand-written field-by-field copy, so a "+
			"column added to the row without a matching response field is silently invisible to every client.",
			respFields, rowFields, rowFields+responseOnlyFields, rowFields, responseOnlyFields)
	}
}
