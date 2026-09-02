package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonKeys returns every json key a struct serializes, following embedded
// structs the way encoding/json does.
func jsonKeys(t reflect.Type) map[string]struct{} {
	out := map[string]struct{}{}
	for _, f := range reflect.VisibleFields(t) {
		if f.Anonymous {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		out[strings.Split(tag, ",")[0]] = struct{}{}
	}
	return out
}

// TestWorkerTaskResponseCarriesEveryTaskResponseField goes RED if the embedding
// is ever replaced by a hand-written copy that drops a field. The endpoint must
// not be able to drift from GET /v1/tasks/{id} on the task's own fields.
func TestWorkerTaskResponseCarriesEveryTaskResponseField(t *testing.T) {
	base := jsonKeys(reflect.TypeOf(taskResponse{}))
	require.NotEmpty(t, base, "control: taskResponse must expose json keys")

	got := jsonKeys(reflect.TypeOf(workerTaskResponse{}))
	for k := range base {
		assert.Contains(t, got, k, "workerTaskResponse must carry taskResponse's %q", k)
	}
	for _, extra := range []string{"job_id", "job_name", "assigned_at", "started_at"} {
		assert.Contains(t, got, extra)
	}
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
