//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doListRequest issues one authenticated GET with rawQuery used verbatim, so a
// repeated or undecodable parameter survives to the handler.
func doListRequest(t *testing.T, srv interface{ Handler() http.Handler }, token, path, rawQuery string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path+"?"+rawQuery, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Error
}

// A NUL byte in any query parameter is a 400, not a 500. The cases span the
// filter branch, the status branch, a page bound and a non-jobs endpoint,
// because the guard is at the shared chokepoint and not in any one of them.
func TestListEndpoints_NulByteInAQueryParameterIs400(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "admin@nulparam.test", true)
	token := createTestToken(t, q, admin.ID)

	cases := []struct{ path, query string }{
		{"/v1/jobs", "status=%00"},
		{"/v1/jobs", "q=%00"},
		{"/v1/jobs", "limit=%00"},
		{"/v1/users", "email=%00"},
	}
	for _, tc := range cases {
		t.Run(tc.path+"?"+tc.query, func(t *testing.T) {
			code, body := doListRequest(t, srv, token, tc.path, tc.query)
			assert.Equal(t, 400, code, "must be rejected before the value reaches a query")
			assert.Equal(t, "query string contains a NUL byte", body)
		})
	}
}

// The handler-level arity call is what covers status and scheduled_job_id;
// parsePage's own call covers only limit, sort and cursor.
func TestListJobs_RepeatedStatusIs400(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Alice", "alice@arity.test", false)
	token := createTestToken(t, q, user.ID)

	code, body := doListRequest(t, srv, token, "/v1/jobs", "status=pending&status=done")
	assert.Equal(t, 400, code)
	assert.Equal(t, `query parameter "status" must appear at most once`, body)
}

// A malformed query string is a 400 on this endpoint, as on every other
// paginated one.
func TestListUsers_MalformedQueryStringIs400(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "admin@userparam.test", true)
	token := createTestToken(t, q, admin.ID)

	code, body := doListRequest(t, srv, token, "/v1/users", "email=x&limit=%zz")
	assert.Equal(t, 400, code)
	assert.Equal(t, "malformed query string", body)
}

func TestListUsers_RepeatedIncludeArchivedIs400(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	admin := createTestUser(t, q, "Admin", "admin@userarity.test", true)
	token := createTestToken(t, q, admin.ID)

	code, body := doListRequest(t, srv, token, "/v1/users", "include_archived=true&include_archived=false")
	require.Equal(t, 400, code)
	assert.Equal(t, `query parameter "include_archived" must appear at most once`, body)
}

// The sort-versus-filter 400 keeps precedence over the arity 400. Both apply
// to sort=name&status=a&status=b, so the input discriminates which guard runs
// first.
func TestListJobs_SortVersusFilterGuardOutranksArity(t *testing.T) {
	srv, q, _ := newTestServerWithPool(t)
	user := createTestUser(t, q, "Alice", "alice@precedence.test", false)
	token := createTestToken(t, q, user.ID)

	code, body := doListRequest(t, srv, token, "/v1/jobs", "sort=name&status=a&status=b")
	require.Equal(t, 400, code)
	assert.Equal(t,
		"sort not supported on filtered list variant; remove the filter or remove the sort",
		body)
}
