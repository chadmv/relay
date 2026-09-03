package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callParseScheduleFilters drives parseScheduleFilters over a raw query string
// and returns the filters, the ok flag, the status code and the decoded error
// body. The arity refusal is the handler's, not this parser's, so a repeated
// parameter is not expressible here; it is pinned through the handler by
// TestListScheduledJobs_FilterErrorsAreReachedThroughTheHandler.
func callParseScheduleFilters(t *testing.T, rawQuery string) (scheduleFilters, bool, *httptest.ResponseRecorder) {
	t.Helper()
	qs, err := url.ParseQuery(rawQuery)
	require.NoError(t, err, "rawQuery must be decodable; malformed input is parsePage's case")
	rec := httptest.NewRecorder()
	f, ok := parseScheduleFilters(rec, qs)
	return f, ok, rec
}

// errBody decodes the {"error": ...} the parser wrote, and fails if it wrote
// nothing: a recorder that was never written to reports 200 by default, so a
// status assertion alone cannot tell a refusal from an untouched recorder.
func errBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	require.NotZero(t, rec.Body.Len(), "the parser must have written a response")
	var body struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Error
}

func TestParseScheduleFilters_AbsentMeansNoFilter(t *testing.T) {
	f, ok, rec := callParseScheduleFilters(t, "")
	require.True(t, ok)
	assert.Zero(t, rec.Body.Len(),
		"an absent filter must leave the response untouched; the recorder reports 200 "+
			"by default, so only an empty body distinguishes that from a refusal")
	assert.Nil(t, f.Q)
	assert.Nil(t, f.Enabled)
}

func TestParseScheduleFilters_ExactErrorBodies(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"enabled not a bool", "enabled=yes", "invalid enabled; expected true or false"},
		{"q not valid UTF-8", "q=%FF%FE", "q is not valid UTF-8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok, rec := callParseScheduleFilters(t, tc.query)
			require.False(t, ok)
			assert.Equal(t, 400, rec.Code)
			assert.Equal(t, tc.want, errBody(t, rec))
		})
	}
}

// enabled is a genuine TRI-STATE and this is the assertion that says so:
// enabled=false must produce a non-nil pointer to false, not a nil pointer. A
// parser that folded false into absent, as ?mine=false is folded on the jobs
// list, would answer "every schedule" to a request for "only paused ones",
// which is a wrong list that looks authoritative.
func TestParseScheduleFilters_EnabledIsTriState(t *testing.T) {
	for _, spelling := range []string{"true", "True", "TRUE", "1", "t"} {
		f, ok, _ := callParseScheduleFilters(t, "enabled="+spelling)
		require.True(t, ok, "spelling=%s", spelling)
		require.NotNil(t, f.Enabled, "spelling=%s", spelling)
		assert.True(t, *f.Enabled, "spelling=%s", spelling)
	}
	for _, spelling := range []string{"false", "False", "FALSE", "0", "f"} {
		f, ok, _ := callParseScheduleFilters(t, "enabled="+spelling)
		require.True(t, ok, "spelling=%s", spelling)
		require.NotNil(t, f.Enabled, "spelling=%s must NOT be folded into absent", spelling)
		assert.False(t, *f.Enabled, "spelling=%s", spelling)
	}
}

func TestParseScheduleFilters_EmptyEnabledIsAbsent(t *testing.T) {
	f, ok, _ := callParseScheduleFilters(t, "enabled=")
	require.True(t, ok)
	assert.Nil(t, f.Enabled, "?enabled= with no value must be treated as absent")
}

// The cap is on runes, not bytes. The needle is built from a rune that encodes
// as two UTF-8 bytes, so 200 of them are 400 bytes and must be accepted, and 201
// must not; a byte-length cap set to 200 rejects both, which is what makes the
// pair discriminate. Both the fixture and the expected message derive from
// maxFilterQRunes and maxFilterQMessage, so this cannot pass by
// hard-coding what the jobs list happens to return today.
func TestParseScheduleFilters_QLengthCapIsInRunes(t *testing.T) {
	twoByteRune := string(rune(0x00e9))
	atCap := strings.Repeat(twoByteRune, maxFilterQRunes)
	overCap := atCap + twoByteRune
	require.Equal(t, 2*maxFilterQRunes, len(atCap),
		"fixture: the needle must be longer in bytes than in runes")

	_, ok, _ := callParseScheduleFilters(t, "q="+url.QueryEscape(atCap))
	assert.True(t, ok, "%d runes must be accepted", maxFilterQRunes)

	_, ok, rec := callParseScheduleFilters(t, "q="+url.QueryEscape(overCap))
	require.False(t, ok)
	assert.Equal(t, 400, rec.Code)
	assert.Equal(t, maxFilterQMessage, errBody(t, rec))
}

func TestParseScheduleFilters_EmptyAndWhitespaceQAreAbsent(t *testing.T) {
	for _, query := range []string{"q=", "q=%20%20%20"} {
		f, ok, _ := callParseScheduleFilters(t, query)
		require.True(t, ok, "query=%s", query)
		assert.Nil(t, f.Q, "query=%s must be treated as an absent q", query)
	}
}

func TestParseScheduleFilters_QIsTrimmed(t *testing.T) {
	f, ok, _ := callParseScheduleFilters(t, "q=%20nightly%20")
	require.True(t, ok)
	require.NotNil(t, f.Q)
	assert.Equal(t, "nightly", *f.Q)
}
