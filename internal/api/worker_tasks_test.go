package api

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonKeys returns every json key a struct serializes, sorted, following
// embedded structs the way encoding/json does.
func jsonKeys(t reflect.Type) []string {
	var out []string
	for _, f := range reflect.VisibleFields(t) {
		if f.Anonymous {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		out = append(out, strings.Split(tag, ",")[0])
	}
	sort.Strings(out)
	return out
}

// TestWorkerTaskResponseCarriesEveryTaskResponseField pins the field set in BOTH
// directions. The subset direction goes RED if the embedding is replaced by a
// hand-written copy that drops a field. The superset direction is the one that
// needs the closed set: workerTaskResponse embeds taskResponse precisely so new
// task fields arrive for free, so a field added to taskResponse reaches this
// endpoint's wire with a zero-line diff in workers.go. The extras are written
// out as a literal rather than derived, because a list derived from the type
// under test agrees with it by construction and can detect nothing.
func TestWorkerTaskResponseCarriesEveryTaskResponseField(t *testing.T) {
	base := jsonKeys(reflect.TypeOf(taskResponse{}))
	require.NotEmpty(t, base, "control: taskResponse must expose json keys")

	want := append(append([]string{}, base...), "job_id", "job_name", "assigned_at", "started_at")
	got := jsonKeys(reflect.TypeOf(workerTaskResponse{}))
	assert.ElementsMatch(t, want, got,
		"workerTaskResponse is taskResponse plus exactly four fields; a new key here reaches "+
			"the wire of GET /v1/workers/{id}/tasks, and a missing one is drift from GET /v1/tasks/{id}")
}

// assignment_epoch is a fence token. Publishing (task id, current epoch) pairs
// for a named worker to any authenticated user hands out exactly the two values
// RequeueTask's comment says a forged status update would otherwise have to
// guess.
func TestWorkerTaskResponseDoesNotDeclareAssignmentEpoch(t *testing.T) {
	got := jsonKeys(reflect.TypeOf(workerTaskResponse{}))
	assert.NotContains(t, got, "assignment_epoch")
}

// The endpoint serves one order. The spec exists so parsePage resolves the
// default and tags the cursor with it; the handler refuses anything else.
func TestWorkerTasksSortSpecAllowsOnlyAssignedAt(t *testing.T) {
	canon, kind, err := parseSort("", WorkerTasksSortSpec)
	require.NoError(t, err)
	assert.Equal(t, "-assigned_at", canon)
	assert.Equal(t, SortKeyTimestamp, kind)

	canon, _, err = parseSort("assigned_at", WorkerTasksSortSpec)
	require.NoError(t, err, "the ascending form must resolve; the handler refuses it, not parseSort")
	assert.Equal(t, "assigned_at", canon)

	for _, bad := range []string{"name", "-name", "status", "created_at", "-started_at", "id"} {
		_, _, err := parseSort(bad, WorkerTasksSortSpec)
		assert.Error(t, err, "sort key %q must be refused", bad)
	}
}
