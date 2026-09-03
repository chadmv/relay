//go:build integration

package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFilterQ_BodiesAreIdenticalAcrossEndpoints compares RAW RESPONSE BYTES, not
// decoded messages, so a difference in encoding or in the surrounding envelope
// is caught too.
//
// It is the guard on the shared parseFilterQ: two endpoints growing their own
// copies of the q rules would drift without either endpoint's own tests
// noticing, because each only tests itself. It cannot detect a change made to
// BOTH endpoints at once, which is the point - that is the shared helper moving
// as one.
func TestFilterQ_BodiesAreIdenticalAcrossEndpoints(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "qparity-admin@test.com", true)
	adminToken := createTestToken(t, q, admin.ID)

	raw := func(path string) (int, string) {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	cases := []struct {
		name  string
		query string
	}{
		{"not valid UTF-8", "?q=%FF%FE"},
		{"too long", "?q=" + strings.Repeat("a", 201)},
		{"repeated", "?q=a&q=b"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jobsCode, jobsBody := raw("/v1/jobs" + tc.query)
			schedCode, schedBody := raw("/v1/scheduled-jobs" + tc.query)

			require.Equal(t, http.StatusBadRequest, jobsCode, "body=%s", jobsBody)
			require.Equal(t, http.StatusBadRequest, schedCode, "body=%s", schedBody)
			assert.NotEmpty(t, jobsBody)
			assert.Equal(t, jobsBody, schedBody,
				"the same malformed q must return a byte-identical body from both endpoints")
		})
	}

	// Control: a well-formed q is accepted by both, so the rows above pin the
	// refusal rather than an endpoint that rejects every q.
	for _, path := range []string{"/v1/jobs?q=needle", "/v1/scheduled-jobs?q=needle"} {
		code, body := raw(path)
		assert.Equal(t, http.StatusOK, code, "path=%s body=%s", path, body)
	}
}
