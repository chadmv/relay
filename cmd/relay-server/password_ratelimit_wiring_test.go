package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/stretchr/testify/require"
)

// putAsUser drives one PUT through the REAL http.Server buildHTTPServer
// returned, authenticated by stubAdminDB with no Postgres.
func putAsUser(t *testing.T, srv *http.Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer any-token-the-stub-resolves")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// passwordBucketServer builds a server whose ONLY configured subsystem is the
// password-change bucket.
//
// LEAVING EVERY OTHER LIMIT FIELD ZERO IS LOAD-BEARING, not incidental: it is
// what makes a crossed assignment in buildHTTPServer
// (s.PasswordChangeLimitN = d.searchLimitN) produce a zero limit, an unarmed
// bucket and a RED test rather than a plausible one.
//
// pool is nil on purpose: every request below is answered before any pool use,
// and stubAdminDB panics on Exec and Query, so a handler that grew a write or a
// multi-row read fails loudly here.
func passwordBucketServer(n int, win time.Duration) *http.Server {
	return buildHTTPServer(httpServerDeps{
		addr:                   "127.0.0.1:0",
		q:                      store.New(stubAdminDB{}),
		passwordChangeLimitN:   n,
		passwordChangeLimitWin: win,
	})
}

// TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit covers four
// separate properties that a source scan does not:
//
//   - the route is wrapped at all;
//   - the composition order puts the limiter AFTER BearerAuth. Written
//     passwordLimit(auth(h)) the limiter runs before anything has put a
//     principal in the context, userRateLimitKey fails closed, and the FIRST
//     request answers 401 instead of 400 - the first assertion below, and the
//     failure names the composition order rather than the ceiling;
//   - the limiter uses the value buildHTTPServer was GIVEN, not a fresh or
//     hard-coded one;
//   - the limiter is constructed ONCE, at Handler() time. Built inside a route
//     closure or per request, every request carries its own empty map and the
//     third answers 400.
//
// THE 400 IS LOAD-BEARING. handleChangePassword refuses `{}` at
// len(NewPassword) < 8, before GetUser and before either bcrypt call, so the
// first two requests prove they reached the real handler with no database at
// all. A 429 there would mean the wired count is smaller than the configured
// one.
func TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit(t *testing.T) {
	srv := passwordBucketServer(2, time.Minute)

	for i := 1; i <= 2; i++ {
		rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"request %d must reach handleChangePassword and be refused by its length guard. "+
				"A 401 here means the limiter sits OUTSIDE the auth chain. body: %s",
			i, rec.Body.String())
	}

	rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"the third request must be refused by the bucket buildHTTPServer was GIVEN. An unwrapped "+
			"route, a hard-coded count, a deleted or crossed assignment and a per-request limiter "+
			"all answer 400 here. body: %s", rec.Body.String())
	// PRESENCE ONLY. The header's VALUE is pinned for this exact middleware by
	// internal/api's TestUserRateLimit_RetryAfterNamesWhenTheWindowActuallyClears,
	// whose band under a one-minute window kills a constant-retry mutation. This
	// slice adds no rate-limit arithmetic, so a second band assertion here would
	// kill nothing.
	require.NotEmpty(t, rec.Header().Get("Retry-After"),
		"a refusal must tell the caller when to come back")
}

// TestBuildHTTPServer_AHumansRetryRunIsNotRefused is the executable form of "a
// normal password change is unaffected" for the case that actually produces a
// burst: a user who mistypes their current password and retries. Five attempts
// inside one minute is more than the two shipped clients can produce by hand -
// the SPA disables its button while the mutation is pending and the CLI asks
// three masked prompts per attempt - so the default ceiling is above anything a
// person reaches.
//
// THE SIXTH REQUEST IS NOT OPTIONAL. Five 400s under a limit of five are also
// what a limiter that does nothing produces, so without it this test is vacuous
// against exactly the implementation it describes.
func TestBuildHTTPServer_AHumansRetryRunIsNotRefused(t *testing.T) {
	srv := passwordBucketServer(5, time.Minute)

	for i := 1; i <= 5; i++ {
		rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"attempt %d of a five-attempt retry run must reach the handler. body: %s",
			i, rec.Body.String())
	}

	over := putAsUser(t, srv, "/v1/users/me/password", `{}`)
	require.Equal(t, http.StatusTooManyRequests, over.Code,
		"the sixth attempt must be refused: without this the five 400s above are also what an "+
			"unwrapped route produces. body: %s", over.Body.String())
}

// TestBuildHTTPServer_ThePasswordBucketIsSeparateFromTheSubmitBucket makes the
// two-buckets decision executable rather than prose, in the lane CI runs.
//
// THE MIDDLE ASSERTION IN EACH DIRECTION IS THE CONTROL. Without proving the
// first bucket is FULL, the final 400 is also what a fixture whose limiter never
// ran produces, and the test would be green for the wrong reason.
//
// Both buckets are set to 1 so a single spend fills either one.
func TestBuildHTTPServer_ThePasswordBucketIsSeparateFromTheSubmitBucket(t *testing.T) {
	build := func() *http.Server {
		return buildHTTPServer(httpServerDeps{
			addr:                   "127.0.0.1:0",
			q:                      store.New(stubAdminDB{}),
			jobSubmitLimitN:        1,
			jobSubmitLimitWin:      time.Minute,
			passwordChangeLimitN:   1,
			passwordChangeLimitWin: time.Minute,
		})
	}

	t.Run("a spent submit budget does not refuse a password change", func(t *testing.T) {
		srv := build()

		spend := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusBadRequest, spend.Code, "body: %s", spend.Body.String())
		over := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusTooManyRequests, over.Code,
			"control: the submit bucket must be provably full before the assertion below means anything")

		rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"a spent submit budget must not refuse a password change: the two buckets bound "+
				"different quantities and sharing one would trade the wrong direction. body: %s",
			rec.Body.String())
	})

	t.Run("a spent password budget does not refuse a job submission", func(t *testing.T) {
		srv := build()

		spend := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, spend.Code, "body: %s", spend.Body.String())
		over := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusTooManyRequests, over.Code,
			"control: the password bucket must be provably full before the assertion below means anything")

		rec := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"a spent password budget must not refuse a job submission. body: %s", rec.Body.String())
	})
}

// TestBuildHTTPServer_AHalfConfiguredPasswordLimitLeavesTheBucketOff pins the
// field pair's promise: zero on EITHER field leaves the bucket off, which is a
// conjunction and not a disjunction.
//
// THE ZERO-COUNT ROW IS THE DISCRIMINATING ONE, and it is placed first because a
// poisoned input read after its target is read by neither the code nor the
// mutant. Relaxing the guard in Server.Handler to an OR constructs a limiter
// whose limit is 0, and rateLimiter.allow takes its over-limit branch on an
// empty window and indexes hits[0], so that row fails loudly on the first
// request. The zero-WINDOW row cannot discriminate against the same relaxation -
// a limiter with a zero window prunes every hit before it counts them and admits
// everything, exactly as no limiter does - and it is here to state the contract
// on the field the count row does not exercise.
func TestBuildHTTPServer_AHalfConfiguredPasswordLimitLeavesTheBucketOff(t *testing.T) {
	cases := []struct {
		name string
		n    int
		win  time.Duration
	}{
		{"window set, count zero", 0, time.Minute},
		{"count set, window zero", 5, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := passwordBucketServer(tc.n, tc.win)

			for i := 1; i <= 3; i++ {
				rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
				require.Equal(t, http.StatusBadRequest, rec.Code,
					"request %d: a half-configured pair must leave the bucket off, so every request "+
						"reaches the handler. body: %s", i, rec.Body.String())
			}
		})
	}
}

// mainBodyOfPackage returns the body of func main, found by parsing every
// non-test .go file in this directory rather than one hardcoded name.
//
// PARSE THE PACKAGE, NOT THE FILE, per the constraint in
// docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md: a
// guard written against "main.go" reports clean after the thing it guards moves
// to a sibling file.
func mainBodyOfPackage(t *testing.T) *ast.BlockStmt {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var body *ast.BlockStmt
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err, "parse %s", name)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name.Name != "main" || fd.Body == nil {
				continue
			}
			require.Nil(t, body, "this package declares func main more than once")
			body = fd.Body
		}
	}
	require.NotNil(t, body, "no func main with a body in any non-test file of this package")
	return body
}

// TestMain_PassesThePasswordChangeLimitItParsed closes the one gap the executed
// tests above cannot reach: they supply the limits themselves, so they say
// nothing about what main puts in the httpServerDeps literal. Zeroing that
// literal, or trading it for another of main's same-typed locals, leaves this
// whole package green while the control is off in production - which is the
// worst available failure for a security control and is not stopped by a
// sentence.
//
// A PARSER GUARD IS THE EXPENSIVE FALLBACK, and it was taken here only because
// the cheaper rung does not exist: main is not callable from a test, and it
// opens the pool and can log.Fatalf before it reaches the literal, so no
// behavioural test in any lane this package has can observe that literal.
//
// DO NOT PASTE ANOTHER COPY OF THIS GUARD FAMILY. These rows belong in the table
// prescribed by
// docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md, and
// they are written in that table's shape - one row per wired field, columns for
// the field, the function its value must derive from, and the env-var literal
// that distinguishes it from a sibling of the same type - so a generalization
// lifts them without redesign.
//
// WHAT IT CANNOT SEE, so its name is not read as more than it checks: a value
// laundered through an intermediate local is followed, but a value TRANSFORMED
// on the way is not - a half-value bound from another local passes every check
// here. It proves the wiring was not deleted, zeroed or crossed. It proves
// nothing about fidelity.
func TestMain_PassesThePasswordChangeLimitItParsed(t *testing.T) {
	body := mainBodyOfPackage(t)

	// from[name] = identifiers AND unquoted string literals its RHS mentions,
	// collected only from assignments that are DIRECT children of main's body,
	// so a parse moved inside an if reaches nothing.
	//
	// ARITY-TOLERANT ON PURPOSE. The parse binds three names from one
	// ParseRateLimit call. A walk that skips len(Lhs) != len(Rhs) - the shape in
	// TestServerCountersIsWiredByMain, correct for its own subject - collects
	// nothing here and fails on correct code.
	//
	// STRING LITERALS ARE COLLECTED ALONGSIDE IDENTIFIERS, and that is what makes
	// the env-var check below possible at all: every rate-limit local is an int
	// or a time.Duration parsed by the same function, so nothing about a value's
	// type or its derivation distinguishes it from a sibling. The only thing that
	// does is the env-var name its chain was parsed from.
	from := map[string][]string{}
	for _, st := range body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok {
			continue
		}
		var rhs []string
		for _, e := range as.Rhs {
			ast.Inspect(e, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					rhs = append(rhs, id.Name)
				}
				if bl, ok := m.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					if s, err := strconv.Unquote(bl.Value); err == nil {
						rhs = append(rhs, s)
					}
				}
				return true
			})
		}
		for _, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				from[id.Name] = append(from[id.Name], rhs...)
			}
		}
	}

	// Every identifier assigned anywhere in main's subtree - ifs, loops,
	// switches and closures included. Derivation alone is defeated by a later
	// assignment of zero inside an if.
	assignedAnywhere := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				assignedAnywhere[id.Name]++
			}
		}
		return true
	})

	// The single buildHTTPServer(httpServerDeps{...}) literal.
	fields := map[string]ast.Expr{}
	calls := 0
	ast.Inspect(body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := ce.Fun.(*ast.Ident)
		if !ok || id.Name != "buildHTTPServer" {
			return true
		}
		calls++
		require.Len(t, ce.Args, 1)
		cl, ok := ce.Args[0].(*ast.CompositeLit)
		require.True(t, ok,
			"buildHTTPServer must be called with an httpServerDeps composite literal at the call "+
				"site, so every dependency is readable there")
		for _, e := range cl.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if k, ok := kv.Key.(*ast.Ident); ok {
				fields[k.Name] = kv.Value
			}
		}
		return true
	})
	require.Equal(t, 1, calls,
		"main must call buildHTTPServer exactly once: called twice the last one decides and this "+
			"guard cannot say which")

	const envVar = "RELAY_PASSWORD_CHANGE_RATE_LIMIT"
	const defaultValue = "5:1m"

	rows := []struct{ field, mustReach, envVar string }{
		{"passwordChangeLimitN", "ParseRateLimit", envVar},
		{"passwordChangeLimitWin", "ParseRateLimit", envVar},
	}

	for _, row := range rows {
		value, present := fields[row.field]
		require.True(t, present,
			"buildHTTPServer is called with no %s field, so the bucket is unarmed in production "+
				"while every test in this package stays green", row.field)

		ident, isIdent := value.(*ast.Ident)
		require.True(t, isIdent,
			"httpServerDeps.%s must be fed a plain identifier, not %T. A literal there is a "+
				"hard-coded bound that %s no longer controls.", row.field, value, row.envVar)

		seen := map[string]bool{}
		queue := []string{ident.Name}
		reachedFn, reachedEnv := false, false
		var otherEnv []string
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			switch {
			case name == row.mustReach:
				reachedFn = true
			case name == row.envVar:
				// Checked BEFORE the RELAY_*_RATE_LIMIT arm below, which would
				// otherwise match this variable's own name.
				reachedEnv = true
			case strings.HasPrefix(name, "RELAY_") && strings.HasSuffix(name, "_RATE_LIMIT"):
				otherEnv = append(otherEnv, name)
			}
			queue = append(queue, from[name]...)
		}

		require.True(t, reachedFn,
			"httpServerDeps.%s is fed %q, which does not derive from %s through an unconditional "+
				"assignment in main's body", row.field, ident.Name, row.mustReach)
		require.True(t, reachedEnv,
			"httpServerDeps.%s is fed %q, whose chain never mentions %s. Both values on this route "+
				"are parsed by the same function from a same-typed sibling variable, so the env-var "+
				"name is the only thing that says WHICH bound arrived.", row.field, ident.Name, row.envVar)
		require.Empty(t, otherEnv,
			"httpServerDeps.%s is fed %q, whose chain reaches %v - another rate limit's variable. "+
				"The password route would then be bounded at some other control's budget.",
			row.field, ident.Name, otherEnv)
		require.Equal(t, 1, assignedAnywhere[ident.Name],
			"%q is assigned %d times inside main. Exactly one unconditional assignment is the whole "+
				"basis on which this test concludes anything: a second one, in an if or a loop, can "+
				"take the wiring back on some deployments while every check above still passes.",
			ident.Name, assignedAnywhere[ident.Name])

		if row.field == "passwordChangeLimitN" {
			// DOC-AND-CODE CONSISTENCY, not a behavioural check: its subject is
			// the README row, which states this default as a number an operator
			// plans against. It cannot tell which of the two strings on that
			// statement is the key and which is the default - both are collected
			// off the same RHS - so it says the pair is present, nothing more.
			require.True(t, seen[defaultValue],
				"main no longer defaults %s to %q, so the README row states a number the binary "+
					"does not use", envVar, defaultValue)
		}
	}
}
