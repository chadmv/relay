package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callParsePage drives parsePage over a raw query string and returns the ok
// flag, the status code and the decoded error body. rawQuery is used verbatim
// so a repeated or undecodable parameter can be expressed.
func callParsePage(t *testing.T, rawQuery string) (pageParams, bool, int, string) {
	t.Helper()
	r := httptest.NewRequest("GET", "/v1/jobs?"+rawQuery, nil)
	rec := httptest.NewRecorder()
	pp, ok := parsePage(rec, r, JobsSortSpec)
	var body struct {
		Error string `json:"error"`
	}
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	}
	return pp, ok, rec.Code, body.Error
}

// r.URL.Query() discards percent-decoding errors and returns whatever it could
// salvage, so an undecodable escape silently became "no filter" and listed the
// whole farm. The two inputs discriminate: %zz alone must not parse as an
// absent parameter, and a=1&q=%zz must not be salvaged into a single-valued q.
func TestParsePage_MalformedQueryStringIsRejected(t *testing.T) {
	for _, raw := range []string{"q=%zz", "limit=10&q=%zz", "sort=%"} {
		t.Run(raw, func(t *testing.T) {
			_, ok, code, body := callParsePage(t, raw)
			require.False(t, ok, "rawQuery=%s must not parse", raw)
			assert.Equal(t, 400, code)
			assert.Equal(t, "malformed query string", body)
		})
	}
}

// The arity rule covers every parameter the handler recognises, not only the
// four filters. Query().Get returns the first of a repeated parameter
// silently, and a silently wrong limit, sort or cursor renders a page that
// looks authoritative.
func TestParsePage_RepeatedPaginationParametersAreRejected(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"limit=10&limit=50", `query parameter "limit" must appear at most once`},
		{"sort=name&sort=-name", `query parameter "sort" must appear at most once`},
		{"cursor=a&cursor=b", `query parameter "cursor" must appear at most once`},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			_, ok, code, body := callParsePage(t, tc.query)
			require.False(t, ok)
			assert.Equal(t, 400, code)
			assert.Equal(t, tc.want, body)
		})
	}
}

// A single occurrence of each still parses, so the guard above rejects arity
// rather than the parameter itself.
func TestParsePage_SingleOccurrenceStillParses(t *testing.T) {
	pp, ok, code, body := callParsePage(t, "limit=10&sort=name")
	require.True(t, ok, "code=%d body=%s", code, body)
	assert.EqualValues(t, 10, pp.Limit)
	assert.Equal(t, "name", pp.Sort)
}
