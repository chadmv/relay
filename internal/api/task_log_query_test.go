package api

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	require.NoError(t, err)
	return v
}

func TestParseTaskLogQuery_DefaultsToAscendingFromZero(t *testing.T) {
	// Every default here is the behaviour handleGetTaskLogs had before the
	// parse was extracted. An absent value and an explicitly empty one resolve
	// the same way, matching how limit and since_seq already treat "".
	for _, raw := range []string{"", "order=", "limit=", "since_seq="} {
		q, err := parseTaskLogQuery(mustQuery(t, raw))
		require.NoError(t, err, "raw=%q", raw)
		assert.Equal(t, taskLogOrderAsc, q.Order, "raw=%q", raw)
		assert.Equal(t, int32(50), q.Limit, "raw=%q", raw)
		assert.Equal(t, int64(0), q.SinceSeq, "raw=%q", raw)
		assert.Equal(t, int64(0), q.BeforeSeq, "raw=%q", raw)
	}
}

func TestParseTaskLogQuery_RejectsAnUnknownOrder(t *testing.T) {
	// An allow-list, not a deny-list: a deny-list fails OPEN on the next value
	// someone adds. The two accepted values travel the same call path in this
	// same table, so the rejections cannot pass by the parser rejecting
	// everything.
	for _, ok := range []struct {
		raw  string
		want taskLogOrder
	}{
		{"order=asc", taskLogOrderAsc},
		{"order=desc", taskLogOrderDesc},
	} {
		q, err := parseTaskLogQuery(mustQuery(t, ok.raw))
		require.NoError(t, err, "raw=%q", ok.raw)
		assert.Equal(t, ok.want, q.Order, "raw=%q", ok.raw)
	}
	for _, bad := range []string{"order=DESC", "order=Asc", "order=descending", "order=-id", "order=1", "order=%20desc"} {
		_, err := parseTaskLogQuery(mustQuery(t, bad))
		require.Error(t, err, "raw=%q", bad)
		assert.Equal(t, "order must be asc or desc", err.Error(), "raw=%q", bad)
	}
}

func TestParseTaskLogQuery_RejectsACursorFromTheWrongDirection(t *testing.T) {
	// Fail closed on a direction-confused client. Silently ignoring a cursor is
	// how a client loops over page 1 forever while believing it is paging.
	_, err := parseTaskLogQuery(mustQuery(t, "order=desc&since_seq=10"))
	require.Error(t, err)
	assert.Equal(t, "since_seq is not valid with order=desc; use before_seq", err.Error())

	// The direction conflict is reported ahead of the value parse, so a client
	// sending both mistakes at once is told the one that explains the other.
	_, err = parseTaskLogQuery(mustQuery(t, "order=desc&since_seq=abc"))
	require.Error(t, err)
	assert.Equal(t, "since_seq is not valid with order=desc; use before_seq", err.Error())

	_, err = parseTaskLogQuery(mustQuery(t, "before_seq=10"))
	require.Error(t, err)
	assert.Equal(t, "before_seq requires order=desc", err.Error())

	_, err = parseTaskLogQuery(mustQuery(t, "order=asc&before_seq=10"))
	require.Error(t, err)
	assert.Equal(t, "before_seq requires order=desc", err.Error())
}

func TestParseTaskLogQuery_RejectsMalformedBeforeSeq(t *testing.T) {
	for _, bad := range []string{"0", "-1", "abc", "1.5", "9223372036854775808"} {
		_, err := parseTaskLogQuery(mustQuery(t, "order=desc&before_seq="+bad))
		require.Error(t, err, "before_seq=%s", bad)
		assert.Equal(t, "before_seq must be a positive integer", err.Error(), "before_seq=%s", bad)
	}
	q, err := parseTaskLogQuery(mustQuery(t, "order=desc&before_seq=1"))
	require.NoError(t, err)
	assert.Equal(t, int64(1), q.BeforeSeq)

	// A cursor is a row id, never an offset. task_logs.id is a table-wide
	// BIGSERIAL, so a large value is ordinary on a busy farm and nothing here
	// may clamp it against total or against the limit.
	q, err = parseTaskLogQuery(mustQuery(t, "order=desc&before_seq=94312&limit=200"))
	require.NoError(t, err)
	assert.Equal(t, int64(94312), q.BeforeSeq)
}

func TestParseTaskLogQuery_LimitClampIsTheSameInBothDirections(t *testing.T) {
	for _, order := range []string{"asc", "desc"} {
		for _, bad := range []string{"0", "201", "-1", "abc"} {
			_, err := parseTaskLogQuery(mustQuery(t, "order="+order+"&limit="+bad))
			require.Error(t, err, "order=%s limit=%s", order, bad)
			assert.Equal(t, "limit must be 1..200", err.Error(), "order=%s limit=%s", order, bad)
		}
		q, err := parseTaskLogQuery(mustQuery(t, "order="+order+"&limit=200"))
		require.NoError(t, err, "order=%s", order)
		assert.Equal(t, int32(200), q.Limit, "order=%s", order)
	}
}

func TestParseTaskLogQuery_DescWithNoCursorIsTheTailRequest(t *testing.T) {
	// The single most common call this feature exists for: "the newest page",
	// with no magic sentinel.
	q, err := parseTaskLogQuery(mustQuery(t, "order=desc&limit=200"))
	require.NoError(t, err)
	assert.Equal(t, taskLogOrderDesc, q.Order)
	assert.Equal(t, int64(0), q.BeforeSeq)
	assert.Equal(t, int64(0), q.SinceSeq)
}

func TestParseTaskLogQuery_AscendingSinceSeqIsUnchanged(t *testing.T) {
	q, err := parseTaskLogQuery(mustQuery(t, "since_seq=41&limit=200"))
	require.NoError(t, err)
	assert.Equal(t, int64(41), q.SinceSeq)
	assert.Equal(t, taskLogOrderAsc, q.Order)

	for _, bad := range []string{"-1", "abc"} {
		_, err := parseTaskLogQuery(mustQuery(t, "since_seq="+bad))
		require.Error(t, err, "since_seq=%s", bad)
		assert.Equal(t, "since_seq must be a non-negative integer", err.Error(), "since_seq=%s", bad)
	}
}
