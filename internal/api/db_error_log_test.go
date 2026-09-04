package api

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"net/http/httptest"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLog redirects the standard logger for the duration of fn.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(prevFlags) })
	fn()
	return buf.String()
}

// A PgError renders its SQLSTATE and the server's Message, and NOTHING ELSE.
// Detail, Hint and Where can echo a parameter value, and the parameter on these
// two routes is caller-supplied text: a needle in a log line is caller input in
// a stream an operator reads, which is how a log pipeline acquires an injected
// field.
//
// The 57014 row goes FIRST because it is the code this slice creates: a
// statement_timeout cancellation is the failure the new control produces and the
// one an operator will be looking for.
func TestListQueryError_RendersSQLStateWithoutDetail(t *testing.T) {
	err := &pgconn.PgError{
		Severity: "ERROR",
		Code:     "57014",
		Message:  "canceling statement due to statement timeout",
		Detail:   "needle=SUPERSECRET",
		Hint:     "hint=SUPERSECRET",
		Where:    "where=SUPERSECRET",
	}
	rec := httptest.NewRecorder()
	out := captureLog(t, func() {
		req := httptest.NewRequest("GET", "/v1/jobs?q=SUPERSECRET", nil)
		listQueryError(rec, req, err, "list jobs failed")
	})

	assert.Contains(t, out, "57014", "the SQLSTATE is what makes a timeout diagnosable")
	assert.Contains(t, out, "canceling statement due to statement timeout")
	assert.Contains(t, out, "/v1/jobs", "the route path locates the failure")
	assert.NotContains(t, out, "SUPERSECRET",
		"neither the pg error's Detail/Hint/Where nor the request's query string may reach the log; "+
			"the needle is caller-supplied text and this line is read by an operator's pipeline")

	assert.Equal(t, 500, rec.Code)
	assert.JSONEq(t, `{"error":"list jobs failed"}`, rec.Body.String(),
		"the response body is UNCHANGED by this slice; mapping 57014 to a distinguishable response "+
			"is explicitly out of scope")
}

func TestListQueryError_NonPgErrorStillLogs(t *testing.T) {
	rec := httptest.NewRecorder()
	out := captureLog(t, func() {
		req := httptest.NewRequest("GET", "/v1/scheduled-jobs", nil)
		listQueryError(rec, req, errRefusedByStub, "list scheduled jobs failed")
	})
	assert.Contains(t, out, "/v1/scheduled-jobs")
	assert.Contains(t, out, errRefusedByStub.Error())
	assert.Equal(t, 500, rec.Code)
}

// The two list handlers must actually USE it. Executed through the real
// handlers, off a stub that refuses every statement - which is the shape a
// timeout takes at this layer: an error out of the query.
func TestListHandlers_LogTheUnderlyingDatabaseError(t *testing.T) {
	db := &countingDB{}
	s := &Server{q: store.New(db)}
	u := searchTestUser(1)

	jobsLog := captureLog(t, func() {
		rec := listJobsAs(t, s, u, "q=needle")
		require.Equal(t, 500, rec.Code)
	})
	assert.Contains(t, jobsLog, errRefusedByStub.Error(),
		"handleListJobs must log the error it turned into a 500, or a tripped statement_timeout is "+
			"indistinguishable from every other database failure and from success followed by silence")

	schedLog := captureLog(t, func() {
		rec := listSchedulesAs(t, s, u, "q=needle")
		require.Equal(t, 500, rec.Code)
	})
	assert.Contains(t, schedLog, errRefusedByStub.Error(),
		"and so must handleListScheduledJobs")
}

// A PARSED guard, and it is parsed because the alternative is 25 executed tests
// for 25 identical branches, most of which are sort arms unreachable without a
// real page of rows.
//
// It covers a NEW arm added later with a bare writeError(...500...), which is
// the realistic regression: someone adds an eleventh sort variant by copying the
// tenth. It does NOT prove the argument passed is the right error, and it does
// not look outside these three functions. That listQueryError itself renders the
// right thing is EXECUTED in TestListQueryError_RendersSQLStateWithoutDetail,
// and that the handlers reach it at all is EXECUTED in
// TestListHandlers_LogTheUnderlyingDatabaseError.
func TestListHandlers_NoBare500Remains(t *testing.T) {
	for file, fns := range map[string][]string{
		"jobs.go":           {"handleListJobs", "listJobsBySort"},
		"scheduled_jobs.go": {"handleListScheduledJobs"},
	} {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		require.NoError(t, err)

		wanted := map[string]bool{}
		for _, n := range fns {
			wanted[n] = true
		}
		seen := map[string]bool{}
		for _, d := range parsed.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !wanted[fd.Name.Name] || fd.Body == nil {
				continue
			}
			seen[fd.Name.Name] = true
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := ce.Fun.(*ast.Ident)
				if !ok || id.Name != "writeError" || len(ce.Args) < 2 {
					return true
				}
				sel, ok := ce.Args[1].(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "StatusInternalServerError" {
					return true
				}
				t.Errorf("%s: %s writes a bare 500 at %s. Use listQueryError so a tripped "+
					"statement_timeout leaves a SQLSTATE in the log instead of being "+
					"indistinguishable from every other database failure.",
					file, fd.Name.Name, fset.Position(ce.Pos()))
				return true
			})
		}
		require.Equal(t, len(wanted), len(seen),
			"%s no longer declares %v; this guard is walking nothing", file, fns)
	}
}
