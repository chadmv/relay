package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testAuthUser() AuthUser {
	id := pgtype.UUID{Valid: true}
	copy(id.Bytes[:], []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
		0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00})
	return AuthUser{ID: id, Email: "caller@example.com"}
}

// callParseJobFilters drives parseJobFilters over a raw query string and
// returns the filters, the ok flag, the status code and the decoded error
// body. rawQuery is used verbatim so a repeated parameter can be expressed.
func callParseJobFilters(t *testing.T, rawQuery string, u AuthUser) (jobFilters, bool, int, string) {
	t.Helper()
	qs, err := url.ParseQuery(rawQuery)
	require.NoError(t, err, "rawQuery must be decodable; malformed input is parsePage's case")
	rec := httptest.NewRecorder()
	f, ok := parseJobFilters(rec, qs, u)
	var body struct {
		Error string `json:"error"`
	}
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	}
	return f, ok, rec.Code, body.Error
}

func TestParseJobFilters_AbsentMeansNoFilter(t *testing.T) {
	f, ok, code, _ := callParseJobFilters(t, "", testAuthUser())
	require.True(t, ok)
	assert.Equal(t, 200, code, "no error must be written")
	assert.Nil(t, f.Q)
	assert.False(t, f.OwnerID.Valid)
	assert.False(t, f.Since.Valid)
	assert.False(t, f.Until.Valid)
}

func TestParseJobFilters_ExactErrorBodies(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"mine not a bool", "mine=yes", "invalid mine; expected true or false"},
		{"since not RFC3339", "since=2026-09-02", "invalid since; expected an RFC3339 timestamp"},
		{"since without offset", "since=2026-09-02T00:00:00", "invalid since; expected an RFC3339 timestamp"},
		{"until not RFC3339", "until=yesterday", "invalid until; expected an RFC3339 timestamp"},
		{"until before since", "since=2026-09-02T02:00:00Z&until=2026-09-02T01:00:00Z", "until is earlier than since"},
		{"q not valid UTF-8", "q=%FF%FE", "q is not valid UTF-8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok, code, body := callParseJobFilters(t, tc.query, testAuthUser())
			require.False(t, ok)
			assert.Equal(t, 400, code)
			assert.Equal(t, tc.want, body)
		})
	}
}

// The cap is on runes, not bytes. U+00E9 encodes as two UTF-8 bytes, so a
// needle at the cap is twice as many bytes as runes and must still be
// accepted, while one rune more must not. A byte-length cap set to the same
// number rejects both, so the pair discriminates. Both fixtures and the
// expected message derive from maxJobFilterQRunes: hard-coding them would let
// the constant move without reddening anything.
//
// The rune is a \u escape the compiler expands, not a raw byte: a non-ASCII
// literal in a source file is unverifiable by eye.
func TestParseJobFilters_QLengthCapIsInRunes(t *testing.T) {
	atCap := strings.Repeat("\u00e9", maxJobFilterQRunes)
	overCap := atCap + "\u00e9"
	require.Equal(t, 2*maxJobFilterQRunes, len(atCap),
		"fixture: the needle must be longer in bytes than in runes")

	_, ok, _, _ := callParseJobFilters(t, "q="+atCap, testAuthUser())
	assert.True(t, ok, "%d runes must be accepted", maxJobFilterQRunes)

	_, ok, code, body := callParseJobFilters(t, "q="+overCap, testAuthUser())
	require.False(t, ok)
	assert.Equal(t, 400, code)
	assert.Equal(t, maxJobFilterQMessage, body)
}

func TestParseJobFilters_EmptyAndWhitespaceQAreAbsent(t *testing.T) {
	for _, query := range []string{"q=", "q=%20%20%20"} {
		f, ok, _, _ := callParseJobFilters(t, query, testAuthUser())
		require.True(t, ok, "query=%s", query)
		assert.Nil(t, f.Q, "query=%s must be treated as an absent q", query)
	}
}

func TestParseJobFilters_QIsTrimmed(t *testing.T) {
	f, ok, _, _ := callParseJobFilters(t, "q=%20render%20", testAuthUser())
	require.True(t, ok)
	require.NotNil(t, f.Q)
	assert.Equal(t, "render", *f.Q)
}

func TestParseJobFilters_MineTrueTakesTheContextUserID(t *testing.T) {
	u := testAuthUser()
	for _, spelling := range []string{"true", "True", "TRUE", "1", "t"} {
		f, ok, _, _ := callParseJobFilters(t, "mine="+spelling, u)
		require.True(t, ok, "spelling=%s", spelling)
		require.True(t, f.OwnerID.Valid, "spelling=%s", spelling)
		assert.Equal(t, u.ID.Bytes, f.OwnerID.Bytes, "spelling=%s", spelling)
	}
}

func TestParseJobFilters_MineFalseIsAbsent(t *testing.T) {
	for _, spelling := range []string{"false", "False", "0", "f"} {
		f, ok, _, _ := callParseJobFilters(t, "mine="+spelling, testAuthUser())
		require.True(t, ok, "spelling=%s", spelling)
		assert.False(t, f.OwnerID.Valid, "mine=%s must mean the same as absent", spelling)
	}
}

// mine=true with no authenticated identity fails closed. Failing open would
// answer with every job on the farm under a URL that says "mine", which is a
// wrong list that looks authoritative.
func TestParseJobFilters_MineWithoutAnAuthenticatedUserFailsClosed(t *testing.T) {
	_, ok, code, body := callParseJobFilters(t, "mine=true", AuthUser{})
	require.False(t, ok)
	assert.Equal(t, 401, code)
	assert.Equal(t, "unauthorized", body)
}

func TestParseJobFilters_EqualBoundsAreALegalEmptyWindow(t *testing.T) {
	f, ok, _, _ := callParseJobFilters(t,
		"since=2026-09-02T01:00:00Z&until=2026-09-02T01:00:00Z", testAuthUser())
	require.True(t, ok)
	require.True(t, f.Since.Valid)
	require.True(t, f.Until.Valid)
	assert.True(t, f.Since.Time.Equal(f.Until.Time))
}

func TestParseJobFilters_AcceptsOffsetsAndFractionalSeconds(t *testing.T) {
	f, ok, _, _ := callParseJobFilters(t,
		"since=2026-09-02T01:00:00.123456%2B02:00&until=2026-09-02T09:00:00Z", testAuthUser())
	require.True(t, ok)
	require.True(t, f.Since.Valid)
	want, err := time.Parse(time.RFC3339Nano, "2026-09-02T01:00:00.123456+02:00")
	require.NoError(t, err)
	assert.True(t, f.Since.Time.Equal(want))
}

// Arity is enforced by rejectRepeatedParams over jobsListArityParams, the
// list handleListJobs itself passes, so a parameter dropped from that list
// reddens this rather than silently taking the first value.
func TestJobsListArity_EveryHandlerParameterIsRejectedWhenRepeated(t *testing.T) {
	require.ElementsMatch(t,
		[]string{"status", "scheduled_job_id", "q", "mine", "since", "until"},
		jobsListArityParams,
		"every parameter handleListJobs reads itself must be arity-checked")

	for _, name := range jobsListArityParams {
		t.Run(name, func(t *testing.T) {
			qs := url.Values{name: {"a", "b"}}
			rec := httptest.NewRecorder()
			ok := rejectRepeatedParams(rec, qs, jobsListArityParams...)
			require.False(t, ok)
			assert.Equal(t, 400, rec.Code)
			var body struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, `query parameter "`+name+`" must appear at most once`, body.Error)
		})
	}
}
