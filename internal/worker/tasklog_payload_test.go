package worker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The task_log SSE payload must be field-identical to internal/api/tasks.go's
// logEntry for seq/stream/content/created_at, plus task_id and job_id, so a
// consumer can define ONE log-line type and merge SSE frames with
// GET /v1/tasks/{id}/logs pages without a translation layer. If you are here
// because you want to add a field: adding one the polling endpoint cannot supply
// breaks that symmetry. step_index/step_total belong to
// docs/backlog/feature-2026-06-26-persist-expose-step-index-total.md.
func TestTaskLogEvent_JSONContract(t *testing.T) {
	ts := time.Date(2026, 8, 9, 14, 36, 25, 123000000, time.UTC)
	b, err := json.Marshal(taskLogEvent{
		TaskID:    "11111111-1111-1111-1111-111111111111",
		JobID:     "22222222-2222-2222-2222-222222222222",
		Seq:       1234,
		Stream:    "stdout",
		Content:   "line one\nline two\n",
		CreatedAt: ts,
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t,
		[]string{"task_id", "job_id", "seq", "stream", "content", "created_at"},
		keys, "exact key set - no extra fields, no renames")

	assert.Equal(t, float64(1234), got["seq"], "seq is a JSON number, matching logEntry.Seq int64")
	assert.Equal(t, "stdout", got["stream"])
	assert.Equal(t, "line one\nline two\n", got["content"])
	assert.Equal(t, "2026-08-09T14:36:25.123Z", got["created_at"], "RFC3339, matching logEntry.CreatedAt time.Time")

	// SSE framing depends on this: handleEvents re-prefixes literal newlines in
	// Data with "data: ", so a payload containing a literal newline would split
	// into multiple data: lines. json.Marshal escapes \n inside strings, so a
	// correctly marshalled payload is exactly one line. This is why the payload
	// must never be built by string concatenation.
	assert.NotContains(t, string(b), "\n")
}
