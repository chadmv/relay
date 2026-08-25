# Auto-Enroll Guards Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Token-less auto-enrollment may CREATE a worker row, never CLAIM an existing one, and never without a fleet ceiling; the enrollment-token path stops overwriting a live credential.

**Architecture:** One new sqlc statement (`InsertWorkerForAutoEnroll`, `ON CONFLICT DO NOTHING`) replaces the lookup-check-upsert triple in `autoEnrollAndRegister`; a `CountWorkers` ceiling check precedes it; `enrollAndRegister` gains a `FOR UPDATE` lookup inside its existing transaction. Refusals are counted on `Handler`, never logged. Default-lane test fixtures are extended first so every guard has a test in the lane CI runs.

**Tech Stack:** Go, pgx/v5, sqlc, gRPC, testify.

**Spec:** `docs/superpowers/specs/2026-08-25-auto-enroll-guards.md`. This plan does not restate it - it cites sections (`spec 5.2`). Its decisions are settled.

**Closes:** `bug-2026-08-12-auto-enroll-hostname-takeover`, `bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded`, `idea-2026-08-25-default-lane-fixture-for-the-enrollment-paths`.

---

## Slice independence declaration

**BACKEND-ONLY, and STRICTLY SEQUENTIAL. There is no frontend slice and no Phase 3 parallelism available.**

- No file under `web/`. No proto change. No migration.
- Every task after Task 1 depends on the fixture surface Tasks 1-2 build, and Tasks 4, 6, 7, 9 all edit
  the same two functions in `internal/worker/handler.go`. Two engineers would collide on one file for
  most of the slice.
- Dispatch **one `relay-backend-engineer`** through Tasks 1-13 in order. Task 14 is a note for the
  conductor, not work.

---

## Scope decision taken at plan time - do not re-litigate

Spec section 7 left the counters-payload sizing to plan time. **The REDUCED form is taken:** refusal
counters live on `Handler` with an exported accessor, modelled on
`internal/worker/taskstatus_fence_counters.go`. **No new `auto_enroll` section on
`GET /v1/server/counters` in this slice.**

Evidence: `internal/api/server_counters.go` is 574 lines with a 1355-line test; a sixth section costs a
const, an accessor, an `api.CounterSources` field, a response struct with json tags,
`counterPayloadLeaves` entries and the section list in that test - and
`cmd/relay-server/counters_wiring_test.go:242` asserts the served section count against
`NumField(api.CounterSources)`, so a new field reddens that guard too. That is comparable to or larger
than the guards which are the point of this slice. Spec 7 states the reduced form satisfies every
acceptance criterion in both items. Task 14 files the HTTP section as its own backlog item.

Consequences: **spec T13 is reshaped** - assert the split through the accessor, not through the
endpoint - and **spec acceptance criterion 6's "published under `auto_enroll`" is met at the accessor**.
Say both in the commit message and in the backlog Resolution notes.

---

## Corrections to the spec, found while planning

Apply these; do not re-derive them.

1. **Spec 8.1's `captureLog` move is unnecessary and would not compile.**
   `handler_tasklog_integration_test.go` is `package worker_test`; the default-lane fixture family is
   `package worker`. They cannot share an unexported helper. A default-lane twin already exists:
   **`captureUnitLog` (`internal/worker/ingest_log_counters_test.go:448`)**, whose own comment says so.
   Tasks 10 uses it. No edit to the integration file at all; spec site 12 loses that bullet.
2. **Spec T1's RED is weaker than it needs to be.** The spec calls it "structural" because the fake is
   scripted per statement and the new statement does not exist at HEAD. Task 1 builds a **semantic**
   responder instead - configured with `existingHostname`, each SQL arm deriving its own answer - so
   T1's test body is byte-identical before and after and its RED is behavioural. This is what keeps
   the test that goes green the same test that was red.
3. **Spec 6.2's `Handler.AutoEnrollWorkerCeiling int` needs a sentinel for "unset".** The spec notes the
   knob has a meaningful `0` but leaves the field an `int`, where the zero value is ambiguous between
   "unset, use 1024" and "explicitly disabled". Task 7 uses `*int` (nil = unset). Anything else makes
   `cmd/relay-server` the only thing that can express "disabled" and leaves every `&Handler{}` test
   unable to.
4. **Spec 12.19 is discharged here in part:** this slice adds and removes no log site, so README's
   ingest-log-budget list should not move. Task 12 still requires READING it; if it mentions
   auto-enroll refusals, that is a finding to report.

---

## File inventory

**Create**

| File | Purpose |
|---|---|
| `internal/worker/handler_enroll_guards_test.go` | default lane: the fixture plus T1, T2, T4-T12 |
| `internal/worker/autoenroll_refusal_counters.go` | reason enum, counter struct, `AutoEnrollRefusalCounts` |
| `internal/worker/autoenroll_refusal_counters_test.go` | dense-run and publish-distinctly guards |
| `cmd/relay-server/autoenroll_config.go` | `parseAutoEnrollCeiling`, `autoEnrollCeilingLine` |
| `cmd/relay-server/autoenroll_config_test.go` | parser table, line test, wiring guard |

**Edit** (additive unless marked)

| File | Change |
|---|---|
| `internal/store/query/workers.sql` | +1 statement; corrected comments at `:56-58` and `:70-81` |
| `internal/store/workers.sql.go` | **generated only** via `make generate` - never hand-edited |
| `internal/worker/handler.go` | `autoEnrollAndRegister:555-619` **rewritten**; `enrollAndRegister:463-535` additive; `errWorkerRevoked:43-45` **replaced**; `Handler` +2 fields +1 accessor; comments `:463-466`, `:555-557`, `:605-616` |
| `internal/worker/handler_register_strand_test.go` | `strandDB` +`script` field, `QueryRow` **rewritten**; header `:23-43` reworded |
| `internal/worker/handler_register_success_test.go` | `fakeTx` +`script`/`execTag`, +`QueryRow`; `Exec` tag line; header `:142-155` reworded |
| `internal/worker/handler_registration_deadline_test.go` | comment `:83-89` reworded (prose only) |
| `internal/worker/handler_auth_test.go` | `TestConnect_AutoEnrollRotatesTokenForExistingHost:517-562` **rewritten** (Task 5) |
| `internal/agent/messages.go:22-24` | **rewritten** default arm |
| `internal/agent/messages_test.go` | assertions on the new arm |
| `cmd/relay-server/main.go` | after `:157`: parse, wire, log the ceiling |
| `README.md` | `:200`, `:290`, `:354`, `:364-368`, `:370-388`, plus new operator content |
| `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md` | dated note (spec 12.16) |
| `docs/superpowers/specs/2026-06-04-auto-enroll-mode-design.md` | dated amendment (spec 12.17) |
| `docs/backlog/bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget.md` | amendment, NOT closure (spec 12.18) |

**Delete:** nothing. `GetWorkerByHostnameForUpdate` stays and gains its new caller (spec 5.4).

---

## Whole-slice verification

```bash
go test ./... -count=1                                     # the lane CI's non-race job runs
go test ./internal/worker/... ./cmd/relay-server/... -count=5
go vet ./...
go vet -tags integration ./...
go test -tags integration -p 1 ./internal/worker/... -timeout 600s   # needs Docker + p4
```

`-count=5` is a flake check and **does not cover data races**. `go test -race` is unrunnable in this
environment (ThreadSanitizer allocation failure, reproduced at `origin/main` on an untouched package);
CI's `race + integration-build` job is the gate and is green on main.

---

## Task list

1. Fixture: statement-aware `QueryRow`, and the first default-lane auto-enroll success (T2)
2. Fixture: the enrollment-token path reaches success and its refusal branch
3. `InsertWorkerForAutoEnroll` + `make generate` (prep)
4. The create-only guard on `autoEnrollAndRegister` (T1, T4)
5. Rewrite `TestConnect_AutoEnrollRotatesTokenForExistingHost` (integration, T3)
6. The live-credential guard on `enrollAndRegister` (T7, T8)
7. The fleet ceiling on the auto-enroll path (T5, T6, T9, T10)
8. `RELAY_AUTO_ENROLL_WORKER_CEILING`: parse, wire, startup line
9. Refusal counters on `Handler`, split by reason (T13 reduced)
10. No log line on refusal; the audit line survives (T11, T12)
11. `authFailureMessage`'s token-less arm
12. Prose: README and the three doc sites
13. Mutation battery
14. Hand-off notes for the conductor

---

### Task 1: Fixture - statement-aware `QueryRow`, and the first default-lane auto-enroll success

Closes spec 2.6 gaps 1 and 2. Nothing else in this slice is testable until this exists.

**Files:** Create `internal/worker/handler_enroll_guards_test.go`; modify
`internal/worker/handler_register_strand_test.go:44-51,109-111` and
`internal/worker/handler_register_success_test.go:89-106`.

- [ ] **Step 1: Write the failing test.** Create the new file. Add a `script *rowScript` field to
  `strandDB` (after `execTag`) and to `fakeTx` (after `execErr`), plus `execTag string` on `fakeTx`.
  **Do NOT write `fakeTx.QueryRow` yet - its absence is the RED.**

```go
package worker

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/metrics"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errRow is a pgx.Row that reports an error and scans nothing. pgx.ErrNoRows
// through this type is how the fixture says "no such row" - which strandDB could
// not express at all before, since its QueryRow returned strandWorkerRow{}
// unconditionally and EVERY :one found a row.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// rowScript answers a QueryRow FROM THE STATE THE FIXTURE IS CONFIGURED WITH,
// not from a per-statement script, and that is load-bearing rather than
// stylistic. A scripted fake must be reconfigured when the handler changes which
// statement it issues, so the test that goes green stops being the test that was
// red. Here a test says "a worker row for this hostname already exists" ONCE and
// every arm derives its answer, so one test body is RED against the
// ON CONFLICT DO UPDATE upsert HEAD issues and GREEN against the DO NOTHING
// insert that replaces it.
//
// MATCHING IS ON SQL TEXT, never on a generated Go symbol, so an arm may name a
// statement that does not exist yet without breaking the build.
type rowScript struct {
	mu sync.Mutex

	// existingHostname, when non-empty, is the single hostname the fake's
	// workers table already holds a row for.
	existingHostname string

	seen []strandExec
}

// answer resolves one QueryRow. First match wins; anything unmatched keeps the
// historical strandWorkerRow, which is what makes this inert for every test in
// the package that predates it.
func (s *rowScript) answer(sql string, args []any) pgx.Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, strandExec{sql: sql, args: args})

	switch {
	// GetWorkerByHostname / GetWorkerByHostnameForUpdate: hostname is $1.
	case strings.Contains(sql, "FROM workers") && strings.Contains(sql, "hostname = $1"):
		if s.existingHostname != "" && strArg(args, 0) == s.existingHostname {
			return strandWorkerRow{}
		}
		return errRow{pgx.ErrNoRows}

	// InsertWorkerForAutoEnroll: name is $1, hostname is $2, and DO NOTHING
	// returns NO ROW on conflict - the whole refusal signal.
	case strings.Contains(sql, "INSERT INTO workers") && strings.Contains(sql, "DO NOTHING"):
		if s.existingHostname != "" && strArg(args, 1) == s.existingHostname {
			return errRow{pgx.ErrNoRows}
		}
		return strandWorkerRow{}
	}
	return strandWorkerRow{}
}

// strArg reads a positional string argument, tolerating pgx's pointer spelling.
func strArg(args []any, i int) string {
	if i >= len(args) {
		return ""
	}
	switch v := args[i].(type) {
	case string:
		return v
	case *string:
		if v != nil {
			return *v
		}
	}
	return ""
}

// queryRowsSeen copies every statement this script answered. A SEPARATE list
// from strandDB.execs and fakeTx.execs, so "no INSERT was issued" stays an exact
// assertion rather than a substring hunt across mixed lists.
func (s *rowScript) queryRowsSeen() []strandExec {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]strandExec, len(s.seen))
	copy(out, s.seen)
	return out
}

func (s *rowScript) sawStatement(substr string) bool {
	for _, q := range s.queryRowsSeen() {
		if strings.Contains(q.sql, substr) {
			return true
		}
	}
	return false
}

// enrollFixture drives Connect down any of the three registration paths with no
// Postgres. It is a SIBLING of newSuccessFixture rather than an option on it:
// that fixture hardcodes a reconnect credential and every test using it depends
// on that, so extending it would give the enrollment paths and the reconnect
// path one constructor and one blast radius.
type enrollFixture struct {
	h       *Handler
	db      *strandDB
	tx      *fakeTx
	script  *rowScript
	stream  *scriptedStream
	events  <-chan events.Event
	release func()
}

type enrollConfig struct {
	hostname         string
	credential       any    // nil = token-less auto-enroll
	existingHostname string
	allowAutoEnroll  bool
	execTag          string // "" keeps fakeTx's historical "DELETE 0"
}

func newEnrollFixture(t *testing.T, cfg enrollConfig) *enrollFixture {
	t.Helper()

	script := &rowScript{existingHostname: cfg.existingHostname}
	db := &strandDB{execTag: "UPDATE 1", script: script}
	tx := &fakeTx{script: script, execTag: cfg.execTag}
	pool := &fakePool{tx: tx}

	grace := NewGraceRegistry(time.Hour, func(string, int32) {})
	t.Cleanup(grace.Stop)

	broker := events.NewBroker()
	evs, unsubscribe := broker.Subscribe(events.Filter{})
	t.Cleanup(unsubscribe)

	h := &Handler{
		q:                   store.New(db),
		pool:                pool,
		registry:            NewRegistry(),
		broker:              broker,
		grace:               grace,
		triggerDispatch:     func() {},
		Metrics:             metrics.NewStore(8),
		AllowAutoEnroll:     cfg.allowAutoEnroll,
		RegistrationTimeout: 5 * time.Second,
	}

	reg := &relayv1.RegisterRequest{Hostname: cfg.hostname, CpuCores: 4, RamGb: 8, Os: "linux"}
	switch c := cfg.credential.(type) {
	case *relayv1.RegisterRequest_EnrollmentToken:
		reg.Credential = c
	case *relayv1.RegisterRequest_AgentToken:
		reg.Credential = c
	}

	s := &scriptedStream{
		ctx:     context.Background(),
		release: make(chan struct{}),
		msgs:    []*relayv1.AgentMessage{{Payload: &relayv1.AgentMessage_Register{Register: reg}}},
	}
	release := sync.OnceFunc(func() { close(s.release) })
	t.Cleanup(release)

	return &enrollFixture{h: h, db: db, tx: tx, script: script, stream: s, events: evs, release: release}
}

// connect runs Connect on its own goroutine and reports whichever outcome
// arrives first: a published worker event (registration SUCCEEDED and Connect is
// parked in the message loop) or Connect returning (registration REFUSED).
func (f *enrollFixture) connect(t *testing.T) (events.Event, error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f.h.Connect(f.stream) }()

	select {
	case ev := <-f.events:
		f.release()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Connect did not return after the stream was torn down")
		}
		return ev, nil
	case err := <-done:
		f.release()
		return events.Event{}, err
	case <-time.After(5 * time.Second):
		f.release()
		t.Fatal("Connect neither published a worker event nor returned within 5s: the registration is " +
			"wedged between authenticateAndRegister and finishRegister, so every assertion below would " +
			"be about a registration that never happened")
		return events.Event{}, nil
	}
}

// TestConnect_AutoEnrollStillCreatesAWorkerForAFreshHostname is the control for
// every refusal test in this file and the first default-lane test in this
// repository to drive a token-less enrollment to a successful return.
//
// tokensSent() == 1 IS THE DISCRIMINATING ASSERTION, not decoration. Asserting
// only that no raw token is retained passes just as well against a build that
// never minted one - the exact vacuity scriptedStream.Send's own comment
// predicted. This is that scrub arm's first consumer.
func TestConnect_AutoEnrollStillCreatesAWorkerForAFreshHostname(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true})

	ev, err := f.connect(t)
	require.NoError(t, err, "a hostname with no worker row must still auto-enroll")
	require.Equal(t, "worker.online", ev.Type)
	require.Equal(t, 1, f.stream.tokensSent(),
		"auto-enrollment must MINT and SEND a fresh agent token; zero here means it was never issued, "+
			"and a test asserting only the secret's absence would pass against that build")

	var resp *relayv1.RegisterResponse
	for _, m := range f.stream.sentMsgs() {
		if rr := m.GetRegisterResponse(); rr != nil {
			resp = rr
		}
	}
	require.NotNil(t, resp)
	assert.Equal(t, "[redacted by scriptedStream]", resp.AgentToken,
		"the retained message must carry the placeholder, which distinguishes 'scrubbed' from 'never sent'")
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/worker/ -run TestConnect_AutoEnrollStillCreatesAWorkerForAFreshHostname -count=1`

Expected: FAIL with a panic, not an assertion:

```
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=... addr=0x0 pc=...]
	relay/internal/store.(*Queries).GetWorkerByHostnameForUpdate(...)
	relay/internal/worker.(*Handler).autoEnrollAndRegister.func1(...)
```

That panic IS spec 2.6 gap 2: `fakeTx` overrides `Exec`, `Commit` and `Rollback` only, so `QueryRow`
falls through to the embedded nil `pgx.Tx`.

- [ ] **Step 3: Implement the two `QueryRow` methods and the configurable tag**

In `handler_register_success_test.go`, immediately after `fakeTx.Exec`:

```go
// QueryRow delegates to the shared rowScript, or - with none - keeps the
// historical strandWorkerRow. EVERY statement in both enrollment transactions is
// a QueryRow on the tx, so without this method those paths are unreachable in
// this lane: it fell through to the embedded nil pgx.Tx and panicked with a bare
// nil dereference one frame inside generated code.
func (tx *fakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if tx.script != nil {
		return tx.script.answer(sql, args)
	}
	return strandWorkerRow{}
}
```

Replace `fakeTx.Exec`'s `return` line (`:105`) with:

```go
	tag := tx.execTag
	if tag == "" {
		// The historical value: zero rows affected, which is what every test
		// predating this field was written against.
		tag = "DELETE 0"
	}
	return pgconn.NewCommandTag(tag), nil
```

Replace `strandDB.QueryRow` (`handler_register_strand_test.go:109-111`):

```go
// QueryRow delegates to the shared rowScript when one is configured. A nil
// script keeps the unconditional strandWorkerRow this used to return, which is
// what makes the change inert for the strand and success tests: they build a
// strandDB without a script and see byte-identical behaviour.
func (d *strandDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if d.script != nil {
		return d.script.answer(sql, args)
	}
	return strandWorkerRow{}
}
```

- [ ] **Step 4: Run the whole package**

Run: `go test ./internal/worker/ -count=1`
Expected: PASS. **The inertness check is that no other test in this package changes result.** Both new
fields default to nil/"" and both delegations fall back. If anything else moves, stop and report.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler_enroll_guards_test.go internal/worker/handler_register_strand_test.go internal/worker/handler_register_success_test.go
git commit -m "test(worker): reach autoEnrollAndRegister's success path in the default lane

fakeTx had no QueryRow, so every statement in both enrollment transactions
panicked on the embedded nil pgx.Tx; strandDB.QueryRow answered every :one with a
populated row, so pgx.ErrNoRows was inexpressible. Both now go through a shared
rowScript keyed on SQL text and configured with fixture STATE rather than a
per-statement script, so a guard test's body need not change when the handler
changes which statement it issues.

Inert for existing tests: a nil script and an empty exec tag both fall back."
```

---

### Task 2: Fixture - the enrollment-token path reaches success and its refusal branch

Closes spec 2.6 gaps 3 and 4 and the fixture item's second criterion.

**Files:** Modify `internal/worker/handler_enroll_guards_test.go`.

- [ ] **Step 1: Write the failing tests.** Append:

```go
// agentEnrollmentRow is a store.AgentEnrollment that is NEITHER consumed NOR
// expired - which strandWorkerRow structurally cannot be. Its type switch does
// cover every field of that struct, but it fills EVERY pgtype.Timestamptz with
// time.Unix(0, 0) and marks it Valid, so an enrollment read through it is
// simultaneously already-consumed and expired in 1970, and enrollAndRegister
// refuses it before its transaction opens.
//
// THE FILL IS POSITIONAL AND THE POSITION IS sqlc's TO CHOOSE.
// store.AgentEnrollment declares CreatedAt, ExpiresAt, ConsumedAt in that order
// and sqlc emits Scan in field order.
// TestAgentEnrollmentRow_IsNeitherConsumedNorExpired scans into the real struct
// and asserts both properties BY NAME, so a reordered or added column reddens
// rather than silently re-pointing these values.
type agentEnrollmentRow struct{}

func (agentEnrollmentRow) Scan(dest ...any) error {
	ts := 0
	for _, d := range dest {
		switch v := d.(type) {
		case *pgtype.UUID:
			*v = strandWorkerID
		case *string:
			*v = "enrollment-token-hash"
		case **string:
			*v = nil
		case *pgtype.Timestamptz:
			switch ts {
			case 0: // CreatedAt
				*v = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
			case 1: // ExpiresAt - must be in the FUTURE
				*v = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
			default: // ConsumedAt - must be INVALID (never consumed)
				*v = pgtype.Timestamptz{}
			}
			ts++
		default:
			return fmt.Errorf("agentEnrollmentRow: no fixture value for scan destination of type %T; "+
				"store.AgentEnrollment gained a column this stub does not model", d)
		}
	}
	return nil
}

func TestAgentEnrollmentRow_IsNeitherConsumedNorExpired(t *testing.T) {
	var e store.AgentEnrollment
	require.NoError(t, agentEnrollmentRow{}.Scan(
		&e.ID, &e.TokenHash, &e.HostnameHint, &e.CreatedBy, &e.CreatedAt, &e.ExpiresAt, &e.ConsumedAt, &e.ConsumedBy))

	assert.False(t, e.ConsumedAt.Valid,
		"a consumed enrollment is refused at handler.go:477, before the transaction opens")
	assert.True(t, e.ExpiresAt.Time.After(time.Now()),
		"an expired enrollment is refused at handler.go:480")
}

// TestConnect_EnrollmentTokenReachesASuccessfulRegistration is the enrollment
// path's GREEN control. Without a non-zero command tag ConsumeAgentEnrollment
// reports zero rows and errEnrollmentNotConsumable fires BY DEFAULT, so the
// fixture could drive that rejection and structurally could not drive a success.
func TestConnect_EnrollmentTokenReachesASuccessfulRegistration(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{
		hostname:   "token-host",
		credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:    "UPDATE 1",
	})

	ev, err := f.connect(t)
	require.NoError(t, err)
	require.Equal(t, "worker.online", ev.Type)
	assert.Equal(t, 1, f.stream.tokensSent(), "enrollment must mint and send a fresh agent token")
}

// TestConnect_EnrollmentTokenIsRefusedWhenTheTokenIsNotConsumable pins
// errEnrollmentNotConsumable somewhere CI executes. The tag is set EXPLICITLY to
// zero rows so this asserts the branch rather than the fixture's default.
func TestConnect_EnrollmentTokenIsRefusedWhenTheTokenIsNotConsumable(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{
		hostname:   "token-host",
		credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:    "UPDATE 0",
	})

	_, err := f.connect(t)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Equal(t, 0, f.stream.tokensSent(), "a refused enrollment must send no agent token")
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/worker/ -run 'TestConnect_EnrollmentToken|TestAgentEnrollmentRow' -count=1`

Expected: `TestAgentEnrollmentRow_IsNeitherConsumedNorExpired` PASS, `...NotConsumable` PASS (today's
default), and `TestConnect_EnrollmentTokenReachesASuccessfulRegistration` FAIL:

```
    Error:      Received unexpected error:
                rpc error: code = Unauthenticated desc = authentication failed
    Test:       TestConnect_EnrollmentTokenReachesASuccessfulRegistration
```

That refusal is spec 2.6 gap 4: `GetAgentEnrollmentByTokenHash` is answered by `strandWorkerRow`, whose
`ConsumedAt` is Valid, so `handler.go:477` refuses.

- [ ] **Step 3: Add the enrollment arm** as the FIRST case of `rowScript.answer`'s switch:

```go
	// GetAgentEnrollmentByTokenHash. strandWorkerRow scans this struct
	// successfully and makes every enrollment look consumed and expired.
	case strings.Contains(sql, "agent_enrollments"):
		return agentEnrollmentRow{}
```

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/worker/ -count=1` -> PASS, all three, nothing else moved.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler_enroll_guards_test.go
git commit -m "test(worker): drive enrollAndRegister to success in the default lane

The shared worker-row stub scanned a store.AgentEnrollment successfully but
filled every timestamp with time.Unix(0,0), so every enrollment token read
through it was both already-consumed and expired; and fakeTx.Exec's fixed
zero-row tag made errEnrollmentNotConsumable the fixture's default outcome. Both
enrollment paths now have a GREEN control in the lane CI runs."
```

---

### Task 3: `InsertWorkerForAutoEnroll` and `make generate`

**A prep task with no behavioural RED, deliberately:** the statement has no caller until Task 4, so
there is nothing to assert beyond its shape. Verification is a build plus a read of the generated file.
Spec 5.2.

**Files:** `internal/store/query/workers.sql` (`:56-58`, `:70-81`, new statement);
`internal/store/workers.sql.go` (generated).

- [ ] **Step 1: Add the statement**, immediately after `UpsertWorkerByHostname` (after `:68`):

```sql
-- name: InsertWorkerForAutoEnroll :one
-- Token-less auto-enrollment's ONLY row-creating statement, and the whole of the
-- create-only rule: enrollment may CREATE a worker and may never CLAIM one.
--
-- DO NOTHING, NOT DO UPDATE, AND NOT A SEPARATE LOOKUP. As a :one this returns
-- pgx.ErrNoRows when the hostname is already taken, whatever the existing row's
-- status and whatever its token - that IS the refusal signal. A
-- SELECT ... FOR UPDATE plus a Go predicate is equivalent for an existing row and
-- NOT equivalent for a fresh one: FOR UPDATE on a hostname that does not exist
-- locks nothing, so two concurrent auto-enrolls of the same fresh hostname both
-- see no row, both proceed, and DO UPDATE lets the loser overwrite the winner's
-- freshly minted token. Check and write are one statement here, so there is no
-- window.
--
-- IT IS NOT UpsertWorkerByHostname AND MUST NOT BECOME IT. That statement stays
-- byte-identical and stays the enrollment-TOKEN path's, where an admin-issued
-- credential authorizes binding to an existing row (see SetWorkerAgentToken).
INSERT INTO workers (name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os, supports_workspaces)
VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(sqlc.narg(supports_workspaces)::bool, TRUE))
ON CONFLICT (hostname) DO NOTHING
RETURNING id;
```

Replace `UpsertWorkerByHostname`'s comment (`:57-58`) with:

```sql
-- Insert a new worker, or bind to the existing row for this hostname and refresh
-- its hardware specs. Admin-managed fields (name, labels, max_slots) are
-- preserved on conflict.
--
-- ITS ONLY CALLER IS enrollAndRegister (internal/worker/handler.go). The old
-- comment said "on reconnect", which was never true - reconnectAndRegister looks
-- the worker up by agent-token hash and calls nothing here - and became more
-- misleading once auto-enroll moved to InsertWorkerForAutoEnroll.
```

Append to `SetWorkerAgentToken`'s comment (after `:78`):

```sql
-- WHICH CALLER MAY REACH IT WITH AN EXISTING ROW: enrollAndRegister only, and
-- only when that row's agent_token_hash is NULL, i.e. it was revoked. That is
-- the revoke-then-re-enroll recovery route. autoEnrollAndRegister can never reach
-- this with an existing row - InsertWorkerForAutoEnroll refuses first.
```

- [ ] **Step 2: Regenerate and follow the CRLF procedure**

```bash
make generate
git diff --ignore-all-space --stat
```

sqlc emits LF and rewrites line endings across every generated file. Keep ONLY the real content change
(`internal/store/workers.sql.go`); revert every LF-only file with `git checkout -- <file>`.

- [ ] **Step 3: Verify the generated file actually changed.** The recorded failure mode is that the CRLF
  revert silently discards the regenerated `.sql.go`, leaving a doc comment contradicting its source.

```bash
grep -n "InsertWorkerForAutoEnroll" internal/store/workers.sql.go
grep -n "ITS ONLY CALLER IS enrollAndRegister" internal/store/workers.sql.go
grep -n "WHICH CALLER MAY REACH IT" internal/store/workers.sql.go
go build ./...
```

Expected: the const, the params struct, the method and BOTH corrected comments present; build clean. An
empty grep means the revert took the content hunk - re-run `make generate`.

- [ ] **Step 4: Confirm `UpsertWorkerByHostname`'s SQL is byte-identical** (item 2's explicit
  constraint), and record its caller count rather than asserting uniqueness:

```bash
git diff -U0 internal/store/query/workers.sql | grep -E '^[-+].*(INSERT INTO workers|DO UPDATE|EXCLUDED)'
grep -rn "UpsertWorkerByHostname" --include=*.go . | wc -l
```

Expected: the first prints only `+` lines from the new statement, never a `-` from the upsert body.
Put the second number in the commit message.

- [ ] **Step 5: Commit**

```bash
git add internal/store/query/workers.sql internal/store/workers.sql.go
git commit -m "feat(store): add InsertWorkerForAutoEnroll, a create-only worker insert

ON CONFLICT (hostname) DO NOTHING RETURNING id, so pgx.ErrNoRows is the refusal
signal for a hostname that already has a row. No caller yet.

UpsertWorkerByHostname's SQL is byte-identical; only its comment moved - it said
'update hardware specs on reconnect', which was never true. Caller count for that
symbol across *.go recorded at <N>."
```

---

### Task 4: The create-only guard on `autoEnrollAndRegister`

Spec 5.1, 5.2, 5.3. Item 1's headline.

**Files:** `internal/worker/handler.go:43-45,555-619`; `internal/worker/handler_enroll_guards_test.go`.

- [ ] **Step 1: Write the failing tests.** Add an `unknownAgentToken bool` field to `rowScript` and this
  arm, before the workers-by-hostname arm:

```go
	// GetWorkerByAgentTokenHash, used by the reconnect path.
	case strings.Contains(sql, "agent_token_hash = $1"):
		if s.unknownAgentToken {
			return errRow{pgx.ErrNoRows}
		}
		return strandWorkerRow{}
```

Then append the tests:

```go
// TestConnect_AutoEnrollRefusesAHostnameThatAlreadyHasAWorkerRow is item 1's
// criterion 1 in the default lane. AUTO-ENROLL MAY CREATE A WORKER AND MAY NEVER
// CLAIM ONE - whatever the existing row's status and whatever its token.
//
// Identity takeover is UPSTREAM of every per-task fence in the tree: a claimant
// that inherits a worker id passes AppendTaskLog's and UpdateTaskStatus's
// worker_id predicates as the task's genuine assignee. Those fences establish
// currency and identity; both are worthless if an attacker can BECOME the
// assignee.
func TestConnect_AutoEnrollRefusesAHostnameThatAlreadyHasAWorkerRow(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{
		hostname:         "taken-host",
		existingHostname: "taken-host",
		allowAutoEnroll:  true,
	})

	_, err := f.connect(t)
	require.Error(t, err, "auto-enroll must never bind to a hostname that already has a worker row")
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	assert.False(t, f.script.sawStatement("DO UPDATE"),
		"the auto-enroll path must not issue UpsertWorkerByHostname at all")
	for _, e := range f.tx.execsSeen() {
		assert.NotContains(t, e.sql, "agent_token_hash = $2",
			"SetWorkerAgentToken must never run: the existing worker's token stays intact")
	}

	// ASSERTING THAT NO STATEMENT WAS ISSUED IS NOT ASSERTING THE TRANSACTION
	// DID NOT COMMIT. Both halves, deliberately.
	commits, rollbacks := f.tx.outcome()
	assert.Equal(t, 0, commits)
	assert.GreaterOrEqual(t, rollbacks, 1)

	// The refusal returns above finishRegister, so no generation is acquired and
	// there is nothing to release. MarkWorkerOfflineIfEpoch is the only Exec on
	// this seam, so an empty list is the cheap check that this stays true.
	assert.Empty(t, f.db.execsSeen())
	assert.Equal(t, 0, f.stream.tokensSent())
}

// TestConnect_AutoEnrollRefusalIsIndistinguishableFromACredentialFailure.
// The two messages are COMPARED WITH EACH OTHER rather than each against a
// literal: comparing literals would still pass if both sites were changed to
// something disclosing.
func TestConnect_AutoEnrollRefusalIsIndistinguishableFromACredentialFailure(t *testing.T) {
	claimed := newEnrollFixture(t, enrollConfig{
		hostname: "taken-host", existingHostname: "taken-host", allowAutoEnroll: true,
	})
	_, claimedErr := claimed.connect(t)
	require.Error(t, claimedErr)

	unknown := newEnrollFixture(t, enrollConfig{
		hostname:   "any-host",
		credential: &relayv1.RegisterRequest_AgentToken{AgentToken: "no-such-token"},
	})
	unknown.script.unknownAgentToken = true
	_, unknownErr := unknown.connect(t)
	require.Error(t, unknownErr)

	assert.Equal(t, status.Code(unknownErr), status.Code(claimedErr))
	assert.Equal(t, status.Convert(unknownErr).Message(), status.Convert(claimedErr).Message())

	msg := status.Convert(claimedErr).Message()
	for _, leak := range []string{"taken-host", "revoked", "exists", "claimed", "ceiling"} {
		assert.NotContains(t, msg, leak,
			"the refusal must disclose nothing about the hostname beyond the refusal itself")
	}
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/worker/ -run 'TestConnect_AutoEnrollRefuses|TestConnect_AutoEnrollRefusal' -count=1`

Expected: BOTH FAIL.
- `...AlreadyHasAWorkerRow`: today the upsert binds to the existing row and the registration succeeds,
  so `connect` returns a nil error and the test stops at
  `Error: An error is expected but got nil. Test: TestConnect_AutoEnrollRefusesAHostnameThatAlreadyHasAWorkerRow`.
- `...IndistinguishableFromACredentialFailure`: same nil-error failure on `require.Error(t, claimedErr)`.
  (Its revoked-message arm becomes reachable only after this task; the M11 mutation in Task 13 is what
  re-proves it.)

- [ ] **Step 3: Implement the guard.** Replace `errWorkerRevoked` (`handler.go:43-45`):

```go
// errHostnameClaimed is returned inside the auto-enroll transaction when a
// workers row for the claimed hostname already exists - whatever its status and
// whatever its token. It replaces errWorkerRevoked, which was a DENY-LIST OF
// EXACTLY ONE STATUS VALUE and failed open on every status added to the
// vocabulary. "A row exists" is a claim about the table rather than about
// today's writers, so it cannot fail open that way, and it removes the status
// vocabulary from this decision entirely.
var errHostnameClaimed = errors.New("hostname already claimed")
```

Replace `autoEnrollAndRegister`'s doc comment (`:555-557`) and its transaction (`:564-603`):

```go
// autoEnrollAndRegister handles token-less enrollment when AllowAutoEnroll is
// set. IT MAY CREATE A WORKER AND MAY NEVER CLAIM ONE: a single
// InsertWorkerForAutoEnroll (ON CONFLICT DO NOTHING) both creates the row and
// refuses a hostname that already has one, with no window between the check and
// the write. It then issues a fresh agent token without consuming any enrollment
// record.
//
// THE REFUSAL IS DELIBERATELY THE SAME status AND THE SAME STRING every other
// credential failure on this surface returns. The previous "worker revoked"
// message told an unauthenticated caller that a row for that hostname existed and
// was revoked - a live hostname-state oracle, and exactly the disclosure the new
// guard must not add a second instance of. The oracle that REMAINS is inherent:
// a caller learns a hostname is claimed because claiming it fails. Refusing
// everything is the only way to close that, and README says so rather than
// claiming the refusal is opaque.
func (h *Handler) autoEnrollAndRegister(...) (string, *workerSender, error) {
	// ... rawAgent/agentHash generation unchanged ...

	var workerID pgtype.UUID
	txErr := pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := h.q.WithTx(tx)

		id, err := txq.InsertWorkerForAutoEnroll(ctx, store.InsertWorkerForAutoEnrollParams{
			Name:               reg.Hostname,
			Hostname:           reg.Hostname,
			CpuCores:           reg.CpuCores,
			RamGb:              reg.RamGb,
			GpuCount:           reg.GpuCount,
			GpuModel:           reg.GpuModel,
			Os:                 reg.Os,
			SupportsWorkspaces: reg.SupportsWorkspaces,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errHostnameClaimed
		}
		if err != nil {
			return fmt.Errorf("insert worker: %w", err)
		}
		workerID = id

		if err := txq.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
			ID: id, AgentTokenHash: &agentHash,
		}); err != nil {
			return fmt.Errorf("set agent token: %w", err)
		}
		return nil
	})
	if txErr != nil {
		if errors.Is(txErr, errHostnameClaimed) {
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
		return "", nil, txErr
	}
	// ... audit log line and finishRegister unchanged ...
}
```

Append to the audit-line comment block (`:605-616`), which is spec site 12.10:

```go
	// AND THE ASYMMETRY WITH THE REFUSAL BELOW IT IS THE ARGUMENT FOR COUNTING
	// RATHER THAN LOGGING. A SUCCESSFUL token-less enrollment is now one line per
	// hostname FOREVER - the hostname can never be auto-enrolled again, because
	// the row it just created refuses the next attempt. A REFUSAL is unboundedly
	// repeatable by the same caller with the same hostname, so it takes a counter
	// and no log site at all (see AutoEnrollRefusals).
```

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/worker/ -count=1` -> PASS.
Also `go build ./...` and check `errWorkerRevoked` is gone: `grep -rn "errWorkerRevoked" internal/`
returns nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_enroll_guards_test.go
git commit -m "fix(worker): auto-enroll may create a worker, never claim one

Naming an in-use hostname token-lessly returned the existing worker's id and
overwrote its agent_token_hash, locking the legitimate agent out and handing the
claimant its registry slot, assignments and reservations. Identity takeover is
upstream of every per-task fence in the tree.

InsertWorkerForAutoEnroll (ON CONFLICT DO NOTHING) replaces the
lookup-check-upsert triple, so check and write are one statement and the
concurrent-first-boot race closes too. errWorkerRevoked is deleted: it was a
deny-list of one status value, and 'worker revoked' was itself a hostname-state
oracle. Every credential refusal on this surface now returns the identical status
and string.

Closes half of bug-2026-08-12-auto-enroll-hostname-takeover."
```

---

### Task 5: Rewrite the integration test that asserts the defect

**THIS IS A TEST THAT PINS A DEFECT, NOT AN ASSERTION WEAKENED TO FIT NEW CODE, AND THE COMMIT MESSAGE
MUST SAY SO** so a reviewer can tell which it is. `TestConnect_AutoEnrollRotatesTokenForExistingHost`
(`handler_auth_test.go:517-562`) requires that re-enrolling an existing host rotates its token and that
the rotated token then authenticates - i.e. it requires the takeover. Spec R7, 10.2.

**Files:** `internal/worker/handler_auth_test.go:517-562` (rewrite in place).

- [ ] **Step 1: Rewrite the test.** Replace lines 517-562 with:

```go
// TestConnect_AutoEnrollRefusesAnExistingHostnameAndLeavesItsTokenIntact is the
// integration arm of item 1's criterion 1, and it is the REWRITE of
// TestConnect_AutoEnrollRotatesTokenForExistingHost. That test asserted the
// takeover as desirable ("re-enrollment should rotate the agent token") and its
// three assertions are inverted here, deliberately: the second token-less enroll
// is refused, the FIRST worker's token still authenticates, and the row's
// agent_token_hash is byte-identical afterwards. It is a defect pinned by a test,
// not an assertion relaxed to fit new code.
func TestConnect_AutoEnrollRefusesAnExistingHostnameAndLeavesItsTokenIntact(t *testing.T) {
	fx := newWorkerTestFixture(t)
	fx.Handler.AllowAutoEnroll = true
	ctx := context.Background()

	// First enrollment: a fresh hostname, so it succeeds and mints a token.
	stream1 := newMockConnectStream(t)
	stream1.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "takeover-host", CpuCores: 4, RamGb: 8, Os: "linux",
			},
		},
	})
	done1 := make(chan error, 1)
	go func() { done1 <- fx.Handler.Connect(stream1) }()
	resp1 := stream1.RecvFromServer(t, 5*time.Second).GetRegisterResponse()
	require.NotNil(t, resp1)
	first := resp1.AgentToken
	require.NotEmpty(t, first)
	stream1.CloseSend()
	<-done1

	before, err := fx.Q.GetWorkerByHostname(ctx, "takeover-host")
	require.NoError(t, err)
	require.NotNil(t, before.AgentTokenHash)

	// Second token-less enroll of the SAME hostname: the takeover attempt.
	stream2 := newMockConnectStream(t)
	stream2.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "takeover-host", CpuCores: 64, RamGb: 512, Os: "linux",
			},
		},
	})
	done2 := make(chan error, 1)
	go func() { done2 <- fx.Handler.Connect(stream2) }()
	err2 := <-done2
	require.Error(t, err2)
	assert.Equal(t, codes.Unauthenticated, status.Code(err2))

	after, err := fx.Q.GetWorkerByHostname(ctx, "takeover-host")
	require.NoError(t, err)
	require.NotNil(t, after.AgentTokenHash)
	assert.Equal(t, *before.AgentTokenHash, *after.AgentTokenHash,
		"the existing worker's agent_token_hash must be byte-identical after a refused takeover")

	// The ORIGINAL agent's token still authenticates. This is the assertion the
	// old test made about the CLAIMANT's token.
	stream3 := newMockConnectStream(t)
	stream3.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "takeover-host", CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: first},
			},
		},
	})
	done3 := make(chan error, 1)
	go func() { done3 <- fx.Handler.Connect(stream3) }()
	require.NotNil(t, stream3.RecvFromServer(t, 5*time.Second).GetRegisterResponse())
	stream3.CloseSend()
	<-done3
}
```

- [ ] **Step 2: Prove it was RED at HEAD.** Before running against the current tree, stash the Task 4
  handler change and run the new test:

```bash
git stash push internal/worker/handler.go internal/store/query/workers.sql internal/store/workers.sql.go
go test -tags integration -p 1 ./internal/worker/ -run TestConnect_AutoEnrollRefusesAnExistingHostnameAndLeavesItsTokenIntact -v -timeout 300s
git stash pop
```

Expected at HEAD: FAIL on `require.Error(t, err2)` - "An error is expected but got nil" - because the
second enroll succeeds. If the stash makes the package not build, instead assert the RED by checking out
`origin/main`'s `handler.go` into a scratch worktree; do NOT skip this step.

- [ ] **Step 3: Run against the current tree**

Run: `go test -tags integration -p 1 ./internal/worker/ -run 'TestConnect_AutoEnroll' -v -timeout 300s`
Expected: PASS, including `TestConnect_AutoEnrollRefusesRevokedWorker` **unedited** (it asserts the code
and the row's status, never the message) and
`TestConnect_AutoEnrollLogLineCannotBeForgedOrFloodedByTheHostname` **unedited** - read its helper
first and confirm it enrolls two DIFFERENT hostnames, so the create-only rule does not break it.

- [ ] **Step 4: Commit**

```bash
git add internal/worker/handler_auth_test.go
git commit -m "test(worker): invert the test that pinned the hostname takeover

TestConnect_AutoEnrollRotatesTokenForExistingHost asserted that re-enrolling an
existing host rotates its token and that the rotated token then authenticates -
i.e. it asserted the takeover as desirable. It is REWRITTEN, not weakened: the
second token-less enroll must now be refused, the FIRST agent's token must still
authenticate, and the row's agent_token_hash must be byte-identical afterwards.
Verified RED against the pre-guard handler before being run green."
```

---

### Task 6: The live-credential guard on `enrollAndRegister`

Spec 5.4. A DIFFERENT predicate from Task 4's, and the asymmetry is forced: a symmetric guard would
block the revoke-then-re-enroll recovery route and leave `delete` - which destroys assignments and
reservations - as the only recovery.

**Files:** `internal/worker/handler.go:463-466,489-525`; `internal/worker/handler_enroll_guards_test.go`.

- [ ] **Step 1: Write the failing tests.** Add the live-row stub and the script field:

```go
// liveWorkerRow is strandWorkerRow with every nullable string column non-NULL,
// which is how this fixture says "this worker holds a LIVE agent token hash".
// strandWorkerRow sets every **string to nil - i.e. a REVOKED row, since
// ClearWorkerAgentToken is the only writer that nulls that column.
//
// It delegates first so it inherits strandWorkerRow's arity check rather than
// re-declaring it.
type liveWorkerRow struct{}

func (liveWorkerRow) Scan(dest ...any) error {
	if err := (strandWorkerRow{}).Scan(dest...); err != nil {
		return err
	}
	for _, d := range dest {
		if v, ok := d.(**string); ok {
			s := "live-agent-token-hash"
			*v = &s
		}
	}
	return nil
}
```

Add `existingHasLiveToken bool` to `rowScript` and `existingHasLiveToken bool` to `enrollConfig`
(threaded into the script in `newEnrollFixture`). In the workers-by-hostname arm, return
`liveWorkerRow{}` instead of `strandWorkerRow{}` when the flag is set.

```go
// TestConnect_EnrollmentTokenRefusesAHostnameWithALiveCredential. The enrollment
// path gets item 1's ORIGINAL predicate - a non-NULL agent_token_hash - because
// on THIS path it genuinely discriminates: NULL means revoked (recovery,
// allowed), non-NULL means a live credential (takeover, refused).
func TestConnect_EnrollmentTokenRefusesAHostnameWithALiveCredential(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{
		hostname:             "live-host",
		existingHostname:     "live-host",
		existingHasLiveToken: true,
		credential:           &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:              "UPDATE 1",
	})

	_, err := f.connect(t)
	require.Error(t, err, "an enrollment token must never overwrite a live agent credential")
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	for _, e := range f.tx.execsSeen() {
		assert.NotContains(t, e.sql, "agent_token_hash = $2", "SetWorkerAgentToken must not run")
	}
	commits, rollbacks := f.tx.outcome()
	assert.Equal(t, 0, commits)
	assert.GreaterOrEqual(t, rollbacks, 1)
	assert.Empty(t, f.db.execsSeen())
	assert.Equal(t, 0, f.stream.tokensSent())
}

// TestConnect_EnrollmentTokenStillEnrollsARevokedHostname is the control, and it
// guards the recovery route the whole slice points operators at. WITHOUT IT the
// pair is defeated by swapping the predicate for a status check - see M9.
func TestConnect_EnrollmentTokenStillEnrollsARevokedHostname(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{
		hostname:         "revoked-host",
		existingHostname: "revoked-host", // exists, and its token hash is NULL
		credential:       &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:          "UPDATE 1",
	})

	ev, err := f.connect(t)
	require.NoError(t, err, "revoke-then-re-enroll is the recovery route this slice documents")
	require.Equal(t, "worker.online", ev.Type)
	assert.Equal(t, 1, f.stream.tokensSent())
}
```

- [ ] **Step 2: Run and watch one fail**

Run: `go test ./internal/worker/ -run 'TestConnect_EnrollmentToken(Refuses|Still)' -count=1`

Expected: `...StillEnrollsARevokedHostname` PASSES (today the path never looks the worker up at all) and
`...RefusesAHostnameWithALiveCredential` FAILS:

```
    Error:      An error is expected but got nil.
    Messages:   an enrollment token must never overwrite a live agent credential
    Test:       TestConnect_EnrollmentTokenRefusesAHostnameWithALiveCredential
```

That the control is already green is the point: the pair distinguishes the predicate, and neither test
alone does.

- [ ] **Step 3: Implement the guard.** Add beside `errHostnameClaimed`:

```go
// errCredentialLive is returned inside the enrollment transaction when the
// existing worker row for this hostname still holds an agent_token_hash. THIS IS
// A DIFFERENT PREDICATE FROM errHostnameClaimed's AND THE DIFFERENCE IS FORCED:
// revoking does not delete the row (ClearWorkerAgentToken nulls the hash and sets
// status='revoked'), so refusing every existing row here would make the revoked
// row block its own recovery and leave `relay workers delete` - which destroys
// assignments and reservations - as the only route. NULL means revoked
// (recovery, allowed); non-NULL means a live credential (takeover, refused).
var errCredentialLive = errors.New("worker credential is live")
```

Make it the FIRST statement of `enrollAndRegister`'s existing `pgx.BeginTxFunc` closure, ahead of the
upsert:

```go
		// FOR UPDATE, INSIDE THE SAME TRANSACTION AS THE UPSERT. The lock is what
		// makes this non-racy for the case that matters, an existing row. For a
		// FRESH hostname it locks nothing, so two admin-issued tokens racing on one
		// brand-new hostname still resolve to one row via ON CONFLICT DO UPDATE -
		// out of the threat model, and disclosed rather than closed.
		//
		// LOCK ORDERING: this transaction takes a workers row lock and then updates
		// an agent_enrollments row. Re-check before adding a caller - at time of
		// writing ConsumeAgentEnrollment has no other caller, so no transaction
		// takes the two in the opposite order and there is no deadlock cycle.
		existing, err := txq.GetWorkerByHostnameForUpdate(ctx, reg.Hostname)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lookup worker: %w", err)
		}
		if err == nil && existing.AgentTokenHash != nil {
			return errCredentialLive
		}
```

And in the `txErr != nil` block, before the `errEnrollmentNotConsumable` arm:

```go
		if errors.Is(txErr, errCredentialLive) {
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
```

Update `enrollAndRegister`'s doc comment (`:463-466`), spec site 12.11:

```go
// enrollAndRegister handles enrollment using an admin-issued enrollment token.
// All DB writes (the FOR UPDATE worker lookup, the worker upsert, the enrollment
// consume, the agent-token set) execute inside a single transaction, so a failure
// anywhere leaves no partial state.
//
// IT REFUSES A HOSTNAME WHOSE WORKER HOLDS A LIVE CREDENTIAL and still binds to
// one whose credential is NULL. The lookup is INSIDE the transaction and holds
// FOR UPDATE, which is what makes the check and the upsert one atomic decision
// for an existing row. Rotating a LIVE agent's credential therefore requires a
// revoke first - same rule, same remedy as the auto-enroll path.
```

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/worker/ -count=1` -> PASS.
Run: `go test -tags integration -p 1 ./internal/worker/ -run TestConnect_EnrollmentToken -timeout 300s`
Expected: PASS, with `TestConnect_EnrollmentTokenRevivesRevokedWorker` green and **unedited** - that is
the strongest single guarantee in this slice that the recovery route survives.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_enroll_guards_test.go
git commit -m "fix(worker): an enrollment token never overwrites a live agent credential

enrollAndRegister had no worker lookup at all - it read the ENROLLMENT record's
ConsumedAt and ExpiresAt and nothing about the worker - so an admin-issued token
naming an in-use hostname rotated that worker's credential and locked its agent
out. A FOR UPDATE lookup now leads the existing transaction and refuses a
non-NULL agent_token_hash.

The predicate is deliberately NOT auto-enroll's 'a row exists': revoking does not
delete the row, so the symmetric guard would block revoke-then-re-enroll and
leave delete - which destroys assignments and reservations - as the only
recovery. TestConnect_EnrollmentTokenRevivesRevokedWorker stays green, unedited."
```

---

### Task 7: The fleet ceiling on the auto-enroll path

Spec 6.2, 6.3. Item 2's headline. **The knob is a `*int`** - see plan correction 3.

**Files:** `internal/worker/handler.go` (const, field, resolver, guard);
`internal/worker/handler_enroll_guards_test.go`.

- [ ] **Step 1: Write the failing tests.** Add a `workerCount int64` field to `rowScript`, a matching
  `workerCount int64` to `enrollConfig`, a `countRow` type, and a `CountWorkers` arm:

```go
// countRow answers a COUNT(*) :one.
type countRow struct{ n int64 }

func (r countRow) Scan(dest ...any) error {
	for _, d := range dest {
		if v, ok := d.(*int64); ok {
			*v = r.n
		}
	}
	return nil
}
```

```go
	// CountWorkers. Matched on the full predicate so it cannot also catch
	// CountRevokedWorkers, which differs only in the comparison operator.
	case strings.Contains(sql, "COUNT(*) FROM workers WHERE status != 'revoked'"):
		return countRow{n: s.workerCount}
```

```go
// TestConnect_AutoEnrollRefusesWhenTheFleetIsAtTheCeiling is item 2's criterion 1.
func TestConnect_AutoEnrollRefusesWhenTheFleetIsAtTheCeiling(t *testing.T) {
	ceiling := 3
	f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true, workerCount: 3})
	f.h.AutoEnrollWorkerCeiling = &ceiling

	_, err := f.connect(t)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// THE CHECK MUST PRECEDE THE WRITE, which is what makes the refusal free of
	// side effects. Asserting only the refusal cannot see a check moved after it.
	assert.False(t, f.script.sawStatement("INSERT INTO workers"),
		"the ceiling check must run BEFORE the insert, so a refused auto-enroll writes nothing")
	assert.Equal(t, 0, f.stream.tokensSent())
}

// TestConnect_AutoEnrollAdmitsOneBelowTheCeiling is the BOUNDARY test, and it is
// what distinguishes >= from >. T5 alone cannot see that one-character mutation.
func TestConnect_AutoEnrollAdmitsOneBelowTheCeiling(t *testing.T) {
	ceiling := 3
	f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true, workerCount: 2})
	f.h.AutoEnrollWorkerCeiling = &ceiling

	ev, err := f.connect(t)
	require.NoError(t, err)
	assert.Equal(t, "worker.online", ev.Type)
}

// TestConnect_AutoEnrollCeilingOfZeroIsDisabled pins the knob's meaningful zero.
func TestConnect_AutoEnrollCeilingOfZeroIsDisabled(t *testing.T) {
	zero := 0
	f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true, workerCount: 100000})
	f.h.AutoEnrollWorkerCeiling = &zero

	_, err := f.connect(t)
	require.NoError(t, err, "0 disables the ceiling; it must not mean a ceiling of zero")
}

// TestConnect_EnrollmentTokenIsNotSubjectToTheCeiling is item 2's criterion 2.
// The ceiling gates the auto-enroll path ONLY, which is what makes "use
// enrollment tokens" the without-downtime answer when the budget is exhausted.
func TestConnect_EnrollmentTokenIsNotSubjectToTheCeiling(t *testing.T) {
	ceiling := 1
	f := newEnrollFixture(t, enrollConfig{
		hostname:    "token-host",
		credential:  &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:     "UPDATE 1",
		workerCount: 100000,
	})
	f.h.AutoEnrollWorkerCeiling = &ceiling

	ev, err := f.connect(t)
	require.NoError(t, err)
	require.Equal(t, "worker.online", ev.Type)
	assert.False(t, f.script.sawStatement("COUNT(*) FROM workers"),
		"the enrollment path must not even ASK the ceiling question")
}

// TestConnect_ReconnectIsRefusedByNeitherGuard is item 2's criterion 3. THE
// "NO INSERT" HALF IS WHAT PROVES the path is row-free rather than merely
// succeeding.
func TestConnect_ReconnectIsRefusedByNeitherGuard(t *testing.T) {
	ceiling := 1
	f := newEnrollFixture(t, enrollConfig{
		hostname:         "strand-host",
		existingHostname: "strand-host",
		credential:       &relayv1.RegisterRequest_AgentToken{AgentToken: "strand-agent-token"},
		workerCount:      100000,
	})
	f.h.AutoEnrollWorkerCeiling = &ceiling

	ev, err := f.connect(t)
	require.NoError(t, err)
	require.Equal(t, "worker.online", ev.Type)
	assert.False(t, f.script.sawStatement("INSERT INTO workers"))
	assert.False(t, f.script.sawStatement("COUNT(*) FROM workers"))
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/worker/ -run 'Ceiling|ReconnectIsRefusedByNeither' -count=1`

Expected: a COMPILE failure first -
`f.h.AutoEnrollWorkerCeiling undefined (type *Handler has no field or method AutoEnrollWorkerCeiling)`.
After Step 3's field exists but before its guard, `...RefusesWhenTheFleetIsAtTheCeiling` fails with
`Error: An error is expected but got nil`. The three controls
(`...AdmitsOneBelowTheCeiling`, `...IsNotSubjectToTheCeiling`, `...ReconnectIsRefusedByNeitherGuard`)
pass at HEAD; their RED is established by mutations M4, M6 and M7 in Task 13, and that is recorded here
rather than discovered later.

- [ ] **Step 3: Implement the knob and the guard.** In `handler.go`, beside `DefaultRegistrationTimeout`:

```go
// DefaultAutoEnrollWorkerCeiling bounds how many non-revoked workers may exist
// before token-less auto-enrollment refuses to create another row. Without it, a
// caller that can reach :9090 under RELAY_ALLOW_AUTO_ENROLL creates one
// permanent row per distinct hostname, forever: the rows outlive their
// connections, survive a restart, and appear in every GET /v1/workers page and
// every dispatcher scan.
//
// 1024 IS DERIVED FROM RELAY_GRPC_MAX_CONNS, AND THE DERIVATION IS NOT AIRTIGHT.
// The two knobs count DIFFERENT QUANTITIES: that one bounds concurrent
// connections, this one bounds total non-revoked rows. A farm of 2000
// intermittently-connected machines with 800 online at a time stays under the
// connection cap and exceeds this ceiling legitimately. Such an operator should
// set this explicitly rather than inherit a number derived from a different
// quantity; 0 disables it.
//
// THE BOUND IS APPROXIMATE AND THE ARITHMETIC IS STATED RATHER THAN IMPLIED. Two
// concurrent auto-enrolls at n == ceiling-1 both pass the check under
// read-committed isolation and both insert, so the true bound is
// ceiling + RELAY_GRPC_MAX_CONNS. Making it exact needs serializable isolation or
// an advisory lock on a hot path, for an overshoot that is a fraction of a
// percent. Do not claim an exact cap anywhere.
const DefaultAutoEnrollWorkerCeiling = 1024
```

On `Handler`, beside `TrailingLogWindow`:

```go
	// AutoEnrollWorkerCeiling bounds non-revoked workers on the auto-enroll path
	// only. Set by cmd/relay-server after construction, from
	// RELAY_AUTO_ENROLL_WORKER_CEILING. Read-only after startup.
	//
	// A *int, NOT AN int, AND THAT DIFFERS DELIBERATELY FROM RegistrationTimeout
	// AND TrailingLogWindow. Those two resolve "non-positive means the default",
	// which works because zero is meaningless for them. THIS KNOB HAS A MEANINGFUL
	// ZERO - disabled - so an int would leave the zero value ambiguous between
	// "unset, use the default" and "explicitly disabled", and only
	// cmd/relay-server could express the difference. nil means the default; a
	// non-nil 0 means DISABLED; a negative value means the default (the parser
	// warns).
	AutoEnrollWorkerCeiling *int
```

```go
// autoEnrollWorkerCeiling resolves the effective ceiling. 0 is a real answer
// here and means disabled - do not "simplify" this to the non-positive-means-
// default rule its two neighbours use.
func (h *Handler) autoEnrollWorkerCeiling() int {
	if h.AutoEnrollWorkerCeiling == nil || *h.AutoEnrollWorkerCeiling < 0 {
		return DefaultAutoEnrollWorkerCeiling
	}
	return *h.AutoEnrollWorkerCeiling
}
```

```go
// errFleetAtCeiling is returned inside the auto-enroll transaction when the
// non-revoked worker count is at or above the ceiling. IT GATES THIS PATH ONLY:
// enrollment tokens are never refused by it, which is what makes "use
// relay agent enroll" the without-downtime answer for an operator whose
// token-less budget is exhausted, and what keeps a bounded refusal from being a
// fleet-wide denial primitive.
var errFleetAtCeiling = errors.New("worker fleet at the auto-enroll ceiling")
```

Inside `autoEnrollAndRegister`'s closure, **before** the insert:

```go
		// BEFORE THE INSERT, which is what makes the refusal free of side effects.
		if ceiling := h.autoEnrollWorkerCeiling(); ceiling > 0 {
			n, err := txq.CountWorkers(ctx)
			if err != nil {
				return fmt.Errorf("count workers: %w", err)
			}
			if n >= int64(ceiling) {
				return errFleetAtCeiling
			}
		}
```

And in the `txErr != nil` block, beside the `errHostnameClaimed` arm:

```go
		if errors.Is(txErr, errFleetAtCeiling) {
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
```

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/worker/ -count=1` -> PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_enroll_guards_test.go
git commit -m "feat(worker): bound token-less auto-enroll with a fleet ceiling

Nothing bounded the total number of worker rows a token-less caller could
create; each distinct hostname bought one permanent row. CountWorkers is now
checked BEFORE the insert, inside the same transaction, and refuses at
DefaultAutoEnrollWorkerCeiling (1024, 0 disables).

The bound is approximate by design: two concurrent auto-enrolls at ceiling-1 both
pass under read-committed, so the honest claim is ceiling + RELAY_GRPC_MAX_CONNS.
The ceiling gates the auto-enroll path only - enrollment tokens and reconnects
never consult it, both pinned by tests.

Closes bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded."
```

---

### Task 8: `RELAY_AUTO_ENROLL_WORKER_CEILING` - parse, wire, startup line

Spec 6.2, 6.4, acceptance criterion 10.

**Files:** Create `cmd/relay-server/autoenroll_config.go` and `..._test.go`; modify
`cmd/relay-server/main.go` after `:157`.

- [ ] **Step 1: Write the failing tests.** `cmd/relay-server/autoenroll_config_test.go`:

```go
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"relay/internal/worker"
)

func TestParseAutoEnrollCeiling(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int
		msgPart string
	}{
		{"unset uses the default and says nothing", "", worker.DefaultAutoEnrollWorkerCeiling, ""},
		{"a positive value is used silently", "50", 50, ""},
		{"zero is ACCEPTED and disables, loudly", "0", 0, "disabled"},
		{"negative uses the default and warns", "-1", worker.DefaultAutoEnrollWorkerCeiling, "not a non-negative integer"},
		{"unparseable uses the default and warns", "lots", worker.DefaultAutoEnrollWorkerCeiling, "not a non-negative integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := parseAutoEnrollCeiling("RELAY_AUTO_ENROLL_WORKER_CEILING", tc.raw)
			require.Equal(t, tc.want, got)
			if tc.msgPart == "" {
				assert.Empty(t, msg)
				return
			}
			assert.Contains(t, msg, tc.msgPart)
		})
	}
}

// TestAutoEnrollCeilingLineIsUnconditionalAndNamesTheDisabledState. A mechanism
// that can refuse an agent must state its limit at every boot, and disabling a
// bound must never be silent.
func TestAutoEnrollCeilingLineIsUnconditionalAndNamesTheDisabledState(t *testing.T) {
	on := autoEnrollCeilingLine(1024, true)
	assert.Contains(t, on, "1024")

	off := autoEnrollCeilingLine(0, true)
	assert.Contains(t, off, "no bound")

	// Auto-enroll itself off: the line must say the ceiling is moot rather than
	// implying a bound is active.
	moot := autoEnrollCeilingLine(1024, false)
	assert.Contains(t, moot, "RELAY_ALLOW_AUTO_ENROLL")
}
```

Plus a wiring guard. **Copy `cmd/relay-server/trailing_log_window_test.go:96-156`
(`TestTrailingLogWindowIsWiredIntoTheHandler`) verbatim into this file**, renaming the test to
`TestAutoEnrollCeilingIsWiredIntoTheHandler`, the field string `"TrailingLogWindow"` to
`"AutoEnrollWorkerCeiling"`, and the function string `"parseTrailingLogWindow"` to
`"parseAutoEnrollCeiling"`. That test parses `main.go` with go/ast and fails if `main()` assigns nothing
to the field or assigns something not derived from the parser - the exact gap that would otherwise leave
the env var dead with every other test green.

- [ ] **Step 2: Run and watch it fail**

Run: `go test ./cmd/relay-server/ -run 'AutoEnroll' -count=1`
Expected: `undefined: parseAutoEnrollCeiling` and `undefined: autoEnrollCeilingLine`, then - once
Step 3's file exists but before main.go is wired - the wiring guard fails with
`main.go assigns nothing to a .AutoEnrollWorkerCeiling field: RELAY_AUTO_ENROLL_WORKER_CEILING is dead and nothing else fails`.

- [ ] **Step 3: Implement.** `cmd/relay-server/autoenroll_config.go`:

```go
package main

import (
	"fmt"
	"strconv"

	"relay/internal/worker"
)

// parseAutoEnrollCeiling resolves RELAY_AUTO_ENROLL_WORKER_CEILING. Same
// three-outcome contract as parseConnLimit and parseWatchdogDuration, and
// deliberately not a log.Fatalf: a bad limit must not stop a server booting when
// a safe default exists.
//
//   - Unset, or a valid positive integer: used as-is, silently.
//   - Exactly zero: ACCEPTED, and the ceiling is disabled. Because disabling a
//     bound must never be silent, this returns an informational line naming what
//     is now unbounded.
//   - Negative or unparseable: the default is used and the message says so. A
//     silently-ignored typo would leave an operator believing they had tightened
//     a bound they had not.
func parseAutoEnrollCeiling(name, raw string) (int, string) {
	if raw == "" {
		return worker.DefaultAutoEnrollWorkerCeiling, ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return worker.DefaultAutoEnrollWorkerCeiling, fmt.Sprintf(
			"%s=%q is not a non-negative integer; using %d", name, raw, worker.DefaultAutoEnrollWorkerCeiling)
	}
	if n == 0 {
		return 0, fmt.Sprintf(
			"%s=%q: the token-less auto-enroll worker ceiling is disabled. A caller that can reach the "+
				"gRPC port creates one permanent workers row per distinct hostname, with no bound.", name, raw)
	}
	return n, ""
}

// autoEnrollCeilingLine renders the unconditional startup line, in the shape of
// watchdogBoundsLine and grpcBoundsLine.
func autoEnrollCeilingLine(ceiling int, allowAutoEnroll bool) string {
	if !allowAutoEnroll {
		return "auto-enroll: disabled (RELAY_ALLOW_AUTO_ENROLL is not set), so the worker ceiling is moot"
	}
	if ceiling <= 0 {
		return "auto-enroll: ENABLED with no bound on worker-row creation. Every distinct hostname a " +
			"caller presents creates one permanent row."
	}
	return fmt.Sprintf(
		"auto-enroll: ENABLED, refusing token-less enrollment at %d non-revoked workers (approximate: two "+
			"concurrent enrolls at the boundary can both pass, so the honest bound is %d + RELAY_GRPC_MAX_CONNS). "+
			"Revoke unused workers to free budget; enrollment tokens are never refused by this ceiling.",
		ceiling, ceiling)
}
```

In `main.go`, immediately after the `RELAY_ALLOW_AUTO_ENROLL` block (`:157`):

```go
	autoEnrollCeiling, autoEnrollCeilingWarning := parseAutoEnrollCeiling(
		"RELAY_AUTO_ENROLL_WORKER_CEILING", os.Getenv("RELAY_AUTO_ENROLL_WORKER_CEILING"))
	if autoEnrollCeilingWarning != "" {
		log.Printf("WARNING: %s", autoEnrollCeilingWarning)
	}
	agentHandler.AutoEnrollWorkerCeiling = &autoEnrollCeiling
	log.Print(autoEnrollCeilingLine(autoEnrollCeiling, agentHandler.AllowAutoEnroll))
```

- [ ] **Step 4: Run to verify**

Run: `go test ./cmd/relay-server/ -count=1` -> PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/autoenroll_config.go cmd/relay-server/autoenroll_config_test.go cmd/relay-server/main.go
git commit -m "feat(server): RELAY_AUTO_ENROLL_WORKER_CEILING and its startup line

Three-outcome parser in the shape of parseConnLimit: unset uses 1024 silently, 0
is accepted and disables the ceiling loudly, negative or unparseable warns and
keeps the default. Never log.Fatalf. The startup line is unconditional and names
the disabled state and the ceiling's approximate arithmetic explicitly.

The wiring guard is a copy of TestTrailingLogWindowIsWiredIntoTheHandler: a
passing parser test proves nothing about main() consuming it."
```

---

### Task 9: Refusal counters on `Handler`, split by reason

Spec 7, reduced form (see the scope decision above). Model: `internal/worker/taskstatus_fence_counters.go`.

**Files:** Create `internal/worker/autoenroll_refusal_counters.go` and `..._test.go`; modify
`internal/worker/handler.go` (field, accessor, three `record` calls).

- [ ] **Step 1: Write the failing tests.** `internal/worker/autoenroll_refusal_counters_test.go`:

```go
package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAutoEnrollRefusalReasonsAreADenseRunFromZero. The values are ARRAY INDICES
// starting at 0, and record fails CLOSED, so a gap or a renumbering is a SILENT
// loss of that reason's counts rather than a panic.
func TestAutoEnrollRefusalReasonsAreADenseRunFromZero(t *testing.T) {
	assert.Equal(t, autoEnrollReason(0), autoEnrollReasonHostnameClaimed)
	assert.Equal(t, autoEnrollReason(1), autoEnrollReasonFleetAtCeiling)
	assert.Equal(t, autoEnrollReason(2), autoEnrollReasonCredentialLive)
	assert.Equal(t, autoEnrollReason(3), autoEnrollReasonCount)
}

// TestAutoEnrollRefusalCounters_EveryReasonIsPublishedDistinctly is the mutation
// M15 detector: incrementing the wrong reason must be visible.
func TestAutoEnrollRefusalCounters_EveryReasonIsPublishedDistinctly(t *testing.T) {
	var c autoEnrollRefusalCounters
	for r := autoEnrollReason(0); r < autoEnrollReasonCount; r++ {
		for i := 0; i <= int(r); i++ {
			c.record(r)
		}
	}
	got := c.snapshot()
	assert.Equal(t, uint64(1), got.HostnameClaimed)
	assert.Equal(t, uint64(2), got.FleetAtCeiling)
	assert.Equal(t, uint64(3), got.CredentialLive)
}

func TestAutoEnrollRefusalCounters_AnOutOfRangeReasonIsDroppedNotPanicked(t *testing.T) {
	var c autoEnrollRefusalCounters
	require.NotPanics(t, func() { c.record(autoEnrollReasonCount) })
	assert.Equal(t, AutoEnrollRefusalCounts{}, c.snapshot())
}
```

Append to `handler_enroll_guards_test.go` a test that each refusal moves its OWN cell:

```go
// TestConnect_EachAutoEnrollRefusalMovesItsOwnCounter. Three refusals that are
// INDISTINGUISHABLE to the caller by design must still be distinguishable to the
// operator, and this is the only place that is true.
func TestConnect_EachAutoEnrollRefusalMovesItsOwnCounter(t *testing.T) {
	claimed := newEnrollFixture(t, enrollConfig{
		hostname: "taken-host", existingHostname: "taken-host", allowAutoEnroll: true,
	})
	_, err := claimed.connect(t)
	require.Error(t, err)
	assert.Equal(t, AutoEnrollRefusalCounts{HostnameClaimed: 1}, claimed.h.AutoEnrollRefusals())

	ceiling := 1
	full := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true, workerCount: 9})
	full.h.AutoEnrollWorkerCeiling = &ceiling
	_, err = full.connect(t)
	require.Error(t, err)
	assert.Equal(t, AutoEnrollRefusalCounts{FleetAtCeiling: 1}, full.h.AutoEnrollRefusals())

	live := newEnrollFixture(t, enrollConfig{
		hostname: "live-host", existingHostname: "live-host", existingHasLiveToken: true,
		credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:    "UPDATE 1",
	})
	_, err = live.connect(t)
	require.Error(t, err)
	assert.Equal(t, AutoEnrollRefusalCounts{CredentialLive: 1}, live.h.AutoEnrollRefusals())
}
```

- [ ] **Step 2: Run and watch it fail**

Run: `go test ./internal/worker/ -run 'AutoEnrollRefusal' -count=1`
Expected: `undefined: autoEnrollReason`, `undefined: autoEnrollRefusalCounters`,
`h.AutoEnrollRefusals undefined (type *Handler has no field or method AutoEnrollRefusals)`.

- [ ] **Step 3: Implement.** `internal/worker/autoenroll_refusal_counters.go`:

```go
package worker

import "sync/atomic"

// autoEnrollReason partitions the refusals the two enrollment guards produce.
//
// THE VALUES ARE ARRAY INDICES AND THEY START AT 0, exactly as
// taskStatusFenceReason does and deliberately unlike logKind, which starts at 1.
// autoEnrollRefusalCounters is a [autoEnrollReasonCount]atomic.Uint64 indexed by
// these constants, so they must stay a DENSE RUN from 0 with the sentinel
// immediately after the last one. record fails CLOSED rather than panicking - it
// runs on the gRPC recv goroutine, which neither Connect nor grpc-go recovers -
// so a gap is a SILENT loss of that reason's counts.
type autoEnrollReason uint8

const (
	// autoEnrollReasonHostnameClaimed: token-less auto-enroll, and a workers row
	// for that hostname already exists. Caller-driven and unboundedly repeatable
	// with the same hostname, which is precisely why this is a counter and not a
	// log line.
	autoEnrollReasonHostnameClaimed autoEnrollReason = iota

	// autoEnrollReasonFleetAtCeiling: token-less auto-enroll, and CountWorkers is
	// at or above RELAY_AUTO_ENROLL_WORKER_CEILING. THE ACTIONABLE ONE: a climbing
	// value means either an attacker filling the budget or a fleet that has
	// genuinely outgrown a default derived from a different quantity. The remedy
	// order is revoke unused workers, then use enrollment tokens (never refused by
	// this ceiling), then raise the knob - which needs a restart.
	autoEnrollReasonFleetAtCeiling

	// autoEnrollReasonCredentialLive: an ADMIN-ISSUED enrollment token naming a
	// hostname whose worker still holds a live agent_token_hash. Not
	// attacker-reachable without an admin credential, so a non-zero value here is
	// far likelier to be an operator rotating a live agent in place - whose remedy
	// is to revoke first - than an attack.
	autoEnrollReasonCredentialLive

	// autoEnrollReasonCount MUST STAY LAST and is NOT a reason. It is the LENGTH
	// of the counter array.
	autoEnrollReasonCount
)

// AutoEnrollRefusalCounts is what the two enrollment guards have refused since
// process start, split by cause.
//
// NO TOTAL, AND THAT IS A DECISION, following TaskStatusFenceCounts: three
// fields that partition the refusals exhaustively make a published total the sum
// of its own siblings, where it can only agree or be a bug. Derive it.
//
// THESE ARE THE ONLY RECORD OF A REFUSAL ANYWHERE. No log site is added on either
// path, deliberately: a log.Printf on an attacker-reachable refusal would be a
// fresh instance of the flood class
// bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget
// describes, on a path whose limiter is not even allocated yet. THE COST, NAMED
// RATHER THAN HIDDEN: a legitimately refused agent - the lost-state-directory
// case - produces no server-side line naming it. The server deliberately cannot
// name an attacker-chosen hostname on an unboundedly repeatable refusal, so an
// operator debugging a refused agent reads the AGENT's exit log, which names its
// own hostname. README says so.
//
// PER REPLICA, monotonic, zeroed by a restart, and never returned to an agent.
// Read through Handler.AutoEnrollRefusals.
type AutoEnrollRefusalCounts struct {
	HostnameClaimed uint64 `json:"hostname_claimed_total"`
	FleetAtCeiling  uint64 `json:"fleet_at_ceiling_total"`
	CredentialLive  uint64 `json:"credential_live_total"`
}

// autoEnrollRefusalCounters is the process-lifetime home. A VALUE field on
// Handler, so the zero value works and every test gets its own. Atomics rather
// than a mutex, for statusFenceCounters' reasons: no container, no cross-field
// invariant (because no total is published), and the increment site is the gRPC
// recv goroutine, whose standing constraint is no new lock, queue, goroutine or
// round trip.
type autoEnrollRefusalCounters struct {
	n [autoEnrollReasonCount]atomic.Uint64
}

// record adds one refusal. Out of range fails CLOSED: losing a count is cheaper
// than a panic on the recv goroutine, which would kill the server process. The
// check is an UPPER BOUND ONLY - autoEnrollReason is uint8, so int(r) cannot be
// negative and a `< 0` arm would be dead code wearing the costume of a control.
func (c *autoEnrollRefusalCounters) record(r autoEnrollReason) {
	i := int(r)
	if i >= len(c.n) {
		return
	}
	c.n[i].Add(1)
}

// snapshot reads the three cells. Adding a reason without adding a line here
// counts it into a cell nobody reads, which
// TestAutoEnrollRefusalCounters_EveryReasonIsPublishedDistinctly turns RED.
func (c *autoEnrollRefusalCounters) snapshot() AutoEnrollRefusalCounts {
	return AutoEnrollRefusalCounts{
		HostnameClaimed: c.n[autoEnrollReasonHostnameClaimed].Load(),
		FleetAtCeiling:  c.n[autoEnrollReasonFleetAtCeiling].Load(),
		CredentialLive:  c.n[autoEnrollReasonCredentialLive].Load(),
	}
}
```

On `Handler`, beside `statusFence`:

```go
	// autoEnrollRefusals counts what the two enrollment guards refused, split by
	// cause. A VALUE, not a pointer, for the same reason its three neighbours are.
	//
	// A FOURTH DISTINCT NOUN, and no input moves more than one of the four. Read
	// through AutoEnrollRefusals. NOT YET ON GET /v1/server/counters - the section
	// is deliberately deferred to its own item; see the plan's scope decision.
	autoEnrollRefusals autoEnrollRefusalCounters
```

```go
// AutoEnrollRefusals reports what this server's enrollment guards have refused
// since process start, split by cause. Per PROCESS, monotonic, and never returned
// to an agent.
func (h *Handler) AutoEnrollRefusals() AutoEnrollRefusalCounts { return h.autoEnrollRefusals.snapshot() }
```

Add one `h.autoEnrollRefusals.record(...)` immediately before each of the three
`status.Errorf(codes.Unauthenticated, "authentication failed")` returns added by Tasks 4, 6 and 7,
with the matching reason.

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/worker/ -count=1` -> PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/autoenroll_refusal_counters.go internal/worker/autoenroll_refusal_counters_test.go internal/worker/handler.go internal/worker/handler_enroll_guards_test.go
git commit -m "feat(worker): count enrollment refusals by cause, and log none of them

Three refusals that are indistinguishable to the caller by design must still be
distinguishable to the operator, and a counter is the only place that can be
true. No log site is added on either path: a log.Printf on an attacker-reachable
refusal would be a fresh instance of the flood class
bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget
describes, on a path whose limiter is not allocated until after registration.

REDUCED FORM, deliberately: counters live on Handler with an accessor and there
is no new GET /v1/server/counters section in this slice. The section is filed as
its own item; spec acceptance criterion 6 is met at the accessor."
```

---

### Task 10: No log line on refusal; the audit line survives

Spec 7, T11, T12. This is the check behind the counting-not-logging decision.

**Files:** `internal/worker/handler_enroll_guards_test.go`.

- [ ] **Step 1: Write the failing tests.** Use `captureUnitLog`
  (`internal/worker/ingest_log_counters_test.go:448`), the default lane's existing log capture - do NOT
  move the integration lane's `captureLog`, which lives in a different package.

```go
// TestConnect_AutoEnrollRefusalWritesNoLogLine. The whole captured log must be
// EMPTY across repeated refusals, so any wording added later reddens this rather
// than passing a substring check. Mirrors
// TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing.
func TestConnect_AutoEnrollRefusalWritesNoLogLine(t *testing.T) {
	logged := captureUnitLog(t)

	for i := 0; i < 5; i++ {
		f := newEnrollFixture(t, enrollConfig{
			hostname: "taken-host", existingHostname: "taken-host", allowAutoEnroll: true,
		})
		_, err := f.connect(t)
		require.Error(t, err)
	}

	assert.Empty(t, logged(),
		"a refusal is unboundedly repeatable by the same caller with the same hostname, and the "+
			"per-connection log limiter is not even allocated until after registration. Refusals are "+
			"COUNTED (Handler.AutoEnrollRefusals), never logged.")
}

// TestConnect_AutoEnrollSuccessStillWritesExactlyOneAuditLine. The success line
// survives unbudgeted BY DECISION, and after the create-only guard the argument
// is stronger than it was: a token-less enrollment is now one line per hostname
// FOREVER, because that hostname can never be auto-enrolled again.
func TestConnect_AutoEnrollSuccessStillWritesExactlyOneAuditLine(t *testing.T) {
	logged := captureUnitLog(t)

	f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true})
	_, err := f.connect(t)
	require.NoError(t, err)

	out := logged()
	assert.Equal(t, 1, strings.Count(out, "auto-enrolled worker"))
	assert.Contains(t, out, `hostname="fresh-host"`, "%q is the injection defence and must stay")
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/worker/ -run 'AutoEnrollRefusalWritesNoLogLine|AuditLine' -count=1`
Expected: BOTH PASS immediately - no log site was ever added, and the audit line was never removed.
**Their RED is established by mutations M12 and M13 in Task 13**, and that is stated here rather than
claimed as a TDD RED it is not.

- [ ] **Step 3: Commit**

```bash
git add internal/worker/handler_enroll_guards_test.go
git commit -m "test(worker): pin the no-log-on-refusal decision and the surviving audit line

Both are green on arrival; their RED is mutation-established (M12 adds a
log.Printf to the refusal, M13 deletes the audit line) and recorded in the plan's
battery rather than claimed as a TDD red."
```

---

### Task 11: `authFailureMessage`'s token-less arm

Spec 12.7 and acceptance criterion 12. The current text is actively misleading under either new guard:
it tells a refused operator to enable a flag that is already enabled.

**Files:** `internal/agent/messages.go:22-24`; `internal/agent/messages_test.go`.

- [ ] **Step 1: Write the failing test.** Append to `internal/agent/messages_test.go`:

```go
// TestAuthFailureMessage_TokenlessArmNamesAllThreeCausesAndBothRemedies. The
// server refuses a token-less registration for three reasons and returns the
// SAME opaque string for all of them, deliberately, so the agent's own exit log
// is the only place an operator can learn what to try. It used to name one cause
// and prescribe enabling a flag that is already enabled whenever the other two
// fire.
func TestAuthFailureMessage_TokenlessArmNamesAllThreeCausesAndBothRemedies(t *testing.T) {
	msg := authFailureMessage(false, false, "/var/lib/relay/token")

	for _, want := range []string{
		"RELAY_ALLOW_AUTO_ENROLL",              // cause 1: the flag is off
		"already has a worker",                 // cause 2: the hostname is claimed
		"ceiling",                              // cause 3: the fleet is at the ceiling
		"relay workers revoke",                 // remedy 1
		"enrollment token",                     // remedy 2
	} {
		assert.Contains(t, msg, want)
	}
	assert.Contains(t, msg, "exiting", "the agent exits rather than reconnect-looping on Unauthenticated")
}
```

- [ ] **Step 2: Run and watch it fail**

Run: `go test ./internal/agent/ -run TestAuthFailureMessage_TokenlessArm -count=1`
Expected: FAIL, four times over:

```
    Error:      "agent: authentication failed - token-less auto-enroll was rejected; the server must have RELAY_ALLOW_AUTO_ENROLL enabled; exiting" does not contain "already has a worker"
```

- [ ] **Step 3: Implement.** Replace the `default` arm (`messages.go:22-24`):

```go
	default:
		return "agent: authentication failed - token-less auto-enroll was rejected. The server returns " +
			"one opaque refusal for all three causes, so check them in order: (1) the server may not have " +
			"RELAY_ALLOW_AUTO_ENROLL enabled; (2) this hostname already has a worker row, and auto-enroll " +
			"creates workers but never claims them - run `relay workers revoke <id>` for it, or enroll with " +
			"an admin-issued enrollment token, which does revive a revoked worker; (3) the fleet may be at " +
			"RELAY_AUTO_ENROLL_WORKER_CEILING, which enrollment tokens are never refused by. exiting"
	}
```

- [ ] **Step 4: Run to verify**

Run: `go test ./internal/agent/ -count=1` -> PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/messages.go internal/agent/messages_test.go
git commit -m "fix(agent): the token-less auth-failure message named one cause of three

It told a refused operator to enable RELAY_ALLOW_AUTO_ENROLL, which is already
enabled whenever either new guard fires. The server's refusal is opaque by
design, so this message is the only place an operator learns what to try - and
the only signal that names the refused hostname at all, since the server
deliberately does not."
```

---

### Task 12: Prose - README and the three doc sites

Spec section 12 and acceptance criteria 11 and 13. Wrong prose has been this repository's dominant
defect class for ten consecutive iterations. **Verify each site by reading the file and grepping the
literal wording, not by reasoning about where the text lives.**

**Files:** `README.md`; the two spec docs; one backlog item.

- [ ] **Step 1: README - the six falsified sentences.** Grep each before editing:

```bash
grep -n "take over an existing worker by claiming its hostname" README.md
grep -n "Takeover is the larger of the two costs" README.md
grep -n "bug-2026-08-12-auto-enroll-hostname-takeover" README.md
grep -n "Revoked workers are the one exception" README.md
grep -n "Nothing bounds the total" README.md
grep -n "Row growth is a deliberate, recorded decision" README.md
```

All six are in `:370-388`. Rewrite that paragraph so that: takeover is refused; the upsert sentence is
deleted (auto-enroll no longer calls that statement); the backlog reference points at
`docs/superpowers/specs/2026-08-25-auto-enroll-guards.md` instead of a now-closed item; "revoked is the
one exception" becomes "every existing row is refused, revoked is no longer distinguished"; **"Nothing
bounds the total." becomes the ceiling, its default, its `0`, and the `ceiling + RELAY_GRPC_MAX_CONNS`
arithmetic**; and "deliberate, recorded decision" becomes "bounded, recorded decision".

**Keep these two verbatim** - both re-confirmed by spec R3: the `RELAY_GRPC_MAX_CONNS_PER_IP` sentence,
and "The enrollment-token path does **not** have this property - the worker upsert and the single-use
token consume share one transaction, so one admin-issued token buys exactly one row".

- [ ] **Step 2: README - the four other sites plus new content**
  - `:200` - add that token-less join works for a hostname with **no existing worker row**, and that a
    machine re-provisioned in place must be revoked first.
  - `:290` - generalise "Revoked workers are not revived" into "an existing worker row is never touched
    at all", and **add a new env-table row for `RELAY_AUTO_ENROLL_WORKER_CEILING`** naming its default,
    its `0`, that it counts ALL non-revoked workers rather than only auto-enrolled ones, and why
    (nothing in the schema records which path created a row).
  - `:354` - add the two new refusal causes and that the agent exits on both.
  - `:364-368` - stays TRUE; add that an enrollment token still revives a revoked worker and no longer
    binds to one whose credential is live.
  - **New:** the operator story (revoke frees budget; enrollment tokens bypass the ceiling; **raising
    the knob requires a restart** - say it, do not imply hot reload), the refusal counters and their
    three causes, that they are NOT yet on `GET /v1/server/counters`, and the diagnosability note that a
    refused agent is diagnosed from the AGENT's log because the server deliberately does not name an
    attacker-chosen hostname on a repeatable refusal. Also state the residual oracle plainly: a caller
    learns a hostname is claimed because claiming it fails; closing that means refusing everything.

- [ ] **Step 3: Read, do not reason about, README's ingest-log-budget list.**

```bash
grep -n "ingest_log_budget" README.md
```

This slice adds and removes no log site, so it should not move. If it mentions auto-enroll refusals,
that is a finding to report, not to fix.

- [ ] **Step 4: The three doc sites**
  - `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md` section 5: add a dated note
    (2026-08-25) that the correction about an auto-enroll attacker seizing an existing worker is now
    closed by `2026-08-25-auto-enroll-guards.md`, rather than leaving a live-sounding threat statement.
  - `docs/superpowers/specs/2026-06-04-auto-enroll-mode-design.md` Non-Goals: a dated amendment that
    hostname takeover is now refused and that per-host allowlisting remains the non-goal it was.
  - `docs/backlog/bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget.md`:
    an **amendment, not a closure** - this slice adds a refusal path with NO log line by decision, a
    third category its two-class census (budgeted / unbudgeted) has no slot for.

- [ ] **Step 5: Verify and commit**

```bash
for s in "take over an existing worker" "Nothing bounds the total" "Revoked workers are the one exception" "Row growth is a deliberate"; do grep -n "$s" README.md; done
```

Expected: no output from any of them.

```bash
git add README.md docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md docs/superpowers/specs/2026-06-04-auto-enroll-mode-design.md docs/backlog/bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget.md
git commit -m "docs: auto-enrollment creates workers and never claims them, and is bounded

Six sentences in README's 'what auto-enrollment costs' paragraph were falsified
by this slice, including the one bug-2026-08-21 named by hand: 'Nothing bounds
the total.' Replaced with the ceiling, its default, its 0 and the honest
ceiling + RELAY_GRPC_MAX_CONNS arithmetic - no exact cap is claimed anywhere.

The residual hostname-existence oracle is stated rather than papered over: a
caller learns a hostname is claimed because claiming it fails, and closing that
would mean refusing everything."
```

---

### Task 13: Mutation battery

**Run in an ISOLATED detached worktree with an isolated scratchpad** - never mutate the shared tree
while sibling agents read it.

```bash
git worktree add --detach ../relay-mutants HEAD
```

**Three rules, all learned here the hard way.**
1. **Verify each mutation actually applied.** This tree is CRLF and four mutations in a row have
   silently failed to apply. After each edit, `git diff --stat` must show the file, and
   `git diff -U0 <file>` must show the intended line.
2. **Run a control that SHOULD die, first.** Uniform results across a battery mean a broken harness,
   not good coverage. Control: change `autoEnrollWorkerCeiling`'s default return to `1`; expect
   `TestConnect_AutoEnrollStillCreatesAWorkerForAFreshHostname` RED.
3. **A poisoned input placed last cannot detect an early-exit mutation.** Where a mutation targets a
   loop or a switch, put the discriminating case FIRST.

| # | Mutation | Must redden |
|---|---|---|
| M1 | `ON CONFLICT (hostname) DO NOTHING` -> `DO UPDATE SET os = EXCLUDED.os` in the new statement (then `make generate`) | `...RefusesAHostnameThatAlreadyHasAWorkerRow`; integration `...LeavesItsTokenIntact` |
| M2 | Drop the `errors.Is(err, pgx.ErrNoRows)` arm; let it fall to the generic error | `...RefusesAHostnameThatAlreadyHasAWorkerRow` (wrong code) and `...IndistinguishableFromACredentialFailure` |
| M3 | Move the ceiling check AFTER the insert | `...RefusesWhenTheFleetIsAtTheCeiling`'s "no INSERT issued" assertion |
| M4 | `n >= int64(ceiling)` -> `n > int64(ceiling)` | **`...AdmitsOneBelowTheCeiling` must stay green and `...RefusesWhenTheFleetIsAtTheCeiling` must redden.** If BOTH redden, the two fixtures are not one apart - fix the fixture, not the test |
| M5 | Invert the disable check: `if ceiling := ...; ceiling >= 0` | `...CeilingOfZeroIsDisabled` |
| M6 | Apply the ceiling to `enrollAndRegister` too | `...EnrollmentTokenIsNotSubjectToTheCeiling` |
| M7 | Apply the ceiling to `reconnectAndRegister` too | `...ReconnectIsRefusedByNeitherGuard` |
| M8 | Delete `GetWorkerByHostnameForUpdate` from `enrollAndRegister` | `...EnrollmentTokenRefusesAHostnameWithALiveCredential` |
| M9 | `existing.AgentTokenHash != nil` -> `existing.Status == "revoked"` | **`...RefusesAHostnameWithALiveCredential` AND `...StillEnrollsARevokedHostname` together.** Either alone survives, which is why the pair exists |
| M10 | Move `enrollAndRegister`'s lookup OUTSIDE its transaction | **A KNOWN SURVIVOR. Record it, do not "fix" it.** It is a source property, not a behaviour the default lane can see. A comment on the lookup holds that position; this tree has just measured what a structural guard costs |
| M11 | Restore `status.Errorf(codes.Unauthenticated, "worker revoked")` on the claimed arm | `...IndistinguishableFromACredentialFailure` |
| M12 | Add `log.Printf("auto-enroll refused for %q", reg.Hostname)` to the claimed arm | `...AutoEnrollRefusalWritesNoLogLine` |
| M13 | Delete the audit line at the end of `autoEnrollAndRegister` | `...SuccessStillWritesExactlyOneAuditLine`; integration `...LogLineCannotBeForgedOrFlooded` |
| M14 | Reuse `UpsertWorkerByHostname` on the auto-enroll path | `...RefusesAHostnameThatAlreadyHasAWorkerRow` |
| M15 | `record(autoEnrollReasonHostnameClaimed)` on the CEILING arm | `...EachAutoEnrollRefusalMovesItsOwnCounter` - the mutation that proves the split is real rather than decorative |
| M16 | `finishRegister(..., "")` from the auto-enroll caller | `...StillCreatesAWorkerForAFreshHostname`'s `tokensSent()` assertion |

- [ ] **Step 1:** Create the detached worktree and run the control mutation; confirm it dies.
- [ ] **Step 2:** Apply M1-M16 one at a time. For each: verify it applied, run
  `go test ./internal/worker/ ./cmd/relay-server/ ./internal/agent/ -count=1`, record RED/GREEN, revert.
- [ ] **Step 3:** For M1 and M13, also run the integration lane arm named above (Docker, `-p 1`).
- [ ] **Step 4:** If any mutation other than M10 survives, **the fix is a permanent test, not a revert.**
  A mutation proof must leave a test behind: the discriminating input has to survive into the suite.
- [ ] **Step 5:** Remove the worktree (`git worktree remove ../relay-mutants`) and record the results
  table in the PR body. **No commit** - the battery produces evidence, not code, unless step 4 fires.

---

### Task 14: Hand-off notes for the conductor

Not implementation. Report these; the conductor runs the commands.

- [ ] **Step 1: Backlog items to CLOSE** via `/backlog close <fragment>` (never by hand-editing
  `status`; the command `git mv`s the file into `docs/backlog/closed/`):
  - `bug-2026-08-12-auto-enroll-hostname-takeover` - Resolution note must record that **item 1's
    acceptance criterion 2 was STRUCK as a request for a regression** (spec R2): auto-enroll does not
    and must not revive a revoked worker.
  - `bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded` - Resolution note must record the
    honest bound, `ceiling + RELAY_GRPC_MAX_CONNS`, not `ceiling`.
  - `idea-2026-08-25-default-lane-fixture-for-the-enrollment-paths` - Resolution note must record that
    its `errWorkerRevoked` criterion was **RESHAPED, not met**: this slice deletes that sentinel, so
    the named branch ceases to exist while the behaviour it described stays asserted.

- [ ] **Step 2: Backlog items to FILE** via `/backlog`:
  1. **`idea`, priority `medium`: publish the auto-enroll refusal counters under `auto_enroll` on
     `GET /v1/server/counters`.** This slice ships the counters on `Handler` with an exported accessor
     and stops there; the section is the checklist (const, accessor, `api.CounterSources` field,
     response struct with json tags, `counterPayloadLeaves`, the section list in
     `internal/api/server_counters_test.go`, and `counters_wiring_test.go:242`'s
     `NumField(api.CounterSources)` arity guard). `worker.AutoEnrollRefusalCounts` already carries its
     json tags, so the section can carry the type directly with no hand-written mapper.
  2. **`idea`, priority `medium`: reap auto-enrolled worker rows that never reconnected.** The
     ceiling's complement, and the only option that helps a deployment already hit. Design questions
     that kept it out of this slice: nothing in the schema records which path created a row
     (`connection_epoch <= 1 AND status != 'online'` is the nearest proxy and also catches
     token-enrolled machines that never returned), and deleting a worker destroys its assignments and
     reservations, so decide `revoke` versus `delete` - `revoke` frees ceiling budget and is
     non-destructive.
  3. **`bug` or `idea`, priority `low`: `reg.Hostname` has no length or charset bound.** Two specific
     things to check: whether Postgres' btree index limit (~2704 bytes) rejects an oversized hostname
     on `hostname TEXT NOT NULL UNIQUE`, and whether `autoEnrollAndRegister` then returns a raw
     Postgres error to an unauthenticated caller. Neither was verified.
  4. **Amendment, not a new item**, to `idea-2026-06-04-cidr-allowlist-auto-enroll`: it remains the
     answer for an operator who can enumerate their networks, and now sits on top of a create-only,
     ceilinged auto-enroll rather than an unbounded one.

- [ ] **Step 3: Report in the PR body**
  - The mutation battery results table, including **M10 as a recorded survivor with its reason**.
  - The `UpsertWorkerByHostname` caller count from Task 3 step 4.
  - That `go test -race` was not run locally and why, and that CI's `race + integration-build` is the gate.
  - Any existing test whose result changed other than the one intentionally rewritten in Task 5 -
    **a finding to report, not to fix**.

---

## Self-review against the spec

- Spec 5.1/5.2 -> Tasks 3, 4. Spec 5.3 -> Task 4 (T4). Spec 5.4 -> Task 6.
- Spec 6.2/6.3 -> Task 7. Spec 6.4 -> Tasks 8, 11, 12.
- Spec 7 -> Tasks 9, 10, reduced per the scope decision; the HTTP section is Task 14 step 2 item 1.
- Spec 8.1 gaps 1-4 -> Tasks 1, 2. Spec 8.3 -> Task 1's `tokensSent()` assertion.
- Spec 10.1 T1-T12 -> Tasks 4, 5, 6, 7, 10 (T13 reshaped into Task 9). Spec 10.2 -> Task 5.
- Spec 10.3 M1-M16 -> Task 13. Spec 12 sites 1-19 -> Task 12, minus site 15's `captureLog` bullet
  (plan correction 1) and plus the fixture-header rewordings folded into Tasks 1-2's files.
- Spec 15 criteria 1-16 -> all covered; 2, 6 and 14 are met in the reshaped forms this plan states
  explicitly rather than silently.
