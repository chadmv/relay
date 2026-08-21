# Ingest-log suppression counts (silent-drop observability, slice 2 of 4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Count what `worker.ingestLogLimiter` drops - separately per kind and per arm (deduped versus budget-suppressed) - and serve those counts as a new `ingest_log_budget` section of the already-shipped admin-only `GET /v1/server/counters`.

**Architecture:** A `[kindCount][2]atomic.Uint64` array lives as a **value field on `*worker.Handler`** (one Handler per process, so process-wide in production, and per-Handler in tests, which is what keeps counts deterministic). `Connect` threads a pointer to it into each connection's stack-local `ingestLogLimiter`, which increments one cell on each of its two suppression arms and nothing on its `l == nil` fail-closed arm. `*worker.Handler` satisfies a new one-method `api.IngestLogBudgetSource`; `cmd/relay-server`'s `buildHTTPServer` wires the same handler identifier that `RegisterAgentServiceServer` serves gRPC on.

**Tech Stack:** Go 1.26, `sync/atomic`, `net/http`, `go/ast` (guards only), testify. No SQL, no migration, no proto, no generated file, no `web/`.

---

## Slice independence declaration

- **FRONTEND WORK: ZERO.** No file under `web/` is created, modified or read. The admin console's Server tab is the eventual consumer of this payload and is explicitly out of scope for all four slices (spec section 14). Phase 3 therefore has **one lane** - `relay-backend-engineer` - and there is no frontend/backend parallelism to schedule.
- **Within the backend the tasks are SEQUENTIAL**, not independent: Task 3 needs Task 2's type, Task 7 needs Task 2's exported snapshot type, Task 8 needs Task 7's interface. The only genuinely independent pair is Task 5 (the `-race` test) and Task 6 (the integration-lane Connect test), which may be done in either order once Task 3 has landed.
- **One item, one spec, one plan, one PR, one session.** There are no multi-session stages here, so nothing in this plan needs filing as `## Stage N` backlog items. `/backlog phases` is NOT needed for this document.
- **Phase 1 is deliberately collapsed into Phase 2.** There is no separate spec doc for this slice: the design was settled by the joint spec `docs/superpowers/specs/2026-08-21-silent-drop-observability.md` (sections 3.3, 7.3, 9, 10.2) and the item was amended on 2026-08-21 with its mechanism constraints. This plan absorbs the spec role for the slice, which is why the verification section below is longer than usual and carries refutations of both documents.

---

## Verification: what was checked, and what is REFUTED

Everything below was read in the worktree `D:/dev/relay/.claude/worktrees/pr-merge-session-961184` at `main` @ `4b97895`. **The item is not a contract and neither is the spec.** Where they disagree with the code, the code wins.

### CONFIRMED

- **C1. `allow` has THREE `return false` paths.** `internal/worker/ingest_log_limiter.go:223-225` (`l == nil`, fail closed), `:258-260` (dedupe), `:265-267` (`tokens == 0`). The item's Summary (lines 15-21) says two; its own 2026-08-21 amendment (lines 106-110) and spec section 3.3 correct it. **Only the last two are events.** The nil arm suppressed nothing because there was no limiter, and it is deliberately unreachable in production (one allocation site, `handler.go:228`).
- **C2. Five kinds, five call sites, three handlers.** `ingest_log_limiter.go:99-105`; call sites `handler.go:743` (`kindBadTaskIDStatus`), `:774` (`kindStatusGetTask`), `:1055` (`kindBadTaskIDLog`), `:1147` (`kindTaskLogPersist`), `:1352` (`kindInventory`). Plus two test-only constructions in `export_test.go:56,88`.
- **C3. The import direction is free.** `internal/api/server.go:13` imports `relay/internal/worker`. `internal/api/server_counters.go:80-91` already records this and contrasts it with `internal/scheduler`, which imports `internal/api` and therefore forces slice 4 to declare its snapshot type in `internal/api`. **This slice may return the worker package's own type.**
- **C4. The `"renumbered freely"` comment exists**, verbatim, at `ingest_log_limiter.go:87-89`. The amendment the item demands is real work, and see R4 for why the item is wrong about half of it.
- **C5. The payload contract is as described.** `internal/api/server_counters.go:10-91` (counts/levels, absent-not-zero, `started_at` always present, no caller-supplied bytes, the de-authorized `swept_by_worker` entry); `server_counters_test.go:497-511` (`counterPayloadAllowList`, one entry), `:516-523` (`counterPayloadLeaves`), `:552-626` (type walk), `:633-687` (bytes walk).
- **C6. The wiring boundary is `cmd/relay-server/http_server.go:85-103`** (`buildHTTPServer`, returns `*http.Server`), called once at `main.go:215-230`. `TestServerCountersIsWiredByMain` (`counters_wiring_test.go:212-395`) ends with the generic property check: **every name on the reachability chain, plus the server binding, must be assigned exactly once across main's entire subtree** (`:368-394`).
- **C7. `agentHandler` is assigned exactly once in main** (`main.go:142`, `:=`). The four later `agentHandler.X = ...` lines (`:143`, `:149`, `:156`, `:196`) have a `SelectorExpr` LHS, which the assignment counter at `counters_wiring_test.go:374-378` does not count. **Adding `agentHandler` to the chain does not break the existing guard.**
- **C8. The counters can never reach an agent.** The only read path is an admin-authenticated HTTP route on `:8080`; `AgentService` has one RPC (`Connect`) and this slice adds no send. Structural, not asserted.

### REFUTED

- **R1. REFUTED - the spec's `[5][2]` array "indexed by `logKind`" does not compile safely.** `logKind`'s constants are `iota + 1` (`ingest_log_limiter.go:99-105`), so the values are **1..5** and `kindInventory == 5` indexes **out of range** of a `[5]` array. That is a panic on the gRPC recv goroutine, which `Connect` has no recover for and grpc-go does not recover either - the exact failure mode the nil arm's own comment (`:219-222`) exists to avoid, reintroduced by the array it is being asked to guard. The obvious repair, indexing `k.kind - 1`, is **worse**: `logKind` is `uint8` (`:89`), so an unset kind 0 wraps to 255 and indexes out of range for a different reason. **Decision: `[kindCount][2]` with a `kindCount` sentinel appended to the const block, direct indexing by the kind, and a bounds check in the record path that fails closed rather than panicking.**
- **R2. REFUTED - "package-level `[5][2]atomic.Uint64` in `internal/worker` ... with a pointer threaded into `ingestLogLimiter`" (item lines 98-104, spec section 7.3).** The sentence is internally inconsistent: a package-level array needs no pointer threaded anywhere. The package-level half is wrong on two independent grounds. **(a) Test isolation.** `internal/worker` has 21 test files in one binary and its flood tests assert **exact** line and (now) drop counts; a process-global counter makes every one of them order-dependent, and there is no `t.Parallel` discipline to lean on (`export_test.go:39-45` says as much). **(b) There is no object for `api.CounterSources` to hold.** The shipped mechanism is a struct of nil-able interface values filtered for typed nils at the wiring boundary (`server_counters.go:99-122`, `http_server.go:92-100`); a package-level counter would have to be wired as a func value or read statically, matching neither. **Decision: the array is a VALUE FIELD on `*worker.Handler`** (`handler.go:140-170`). It is process-wide in production because `RegisterAgentServiceServer(grpcSrv, agentHandler)` is called once with one Handler (`main.go:199`), and that is a property the wiring guard can *check* rather than a property a package-level var merely asserts.
- **R3. REFUTED - "Values may still be renumbered; renaming a kind changes a JSON key" (item lines 115-119).** Half right. Once the values are array indices they may **not** be renumbered freely: they must stay a dense run starting at 1, with the sentinel last, or `record` silently drops that kind. **Both halves of the comment are replaced, not just the names half.**
- **R4. REFUTED (partially) - slice 1's atomics-to-mutex reasoning does NOT transfer.** `netlimit` converted `refusedTotal`/`refusedPerIP` from `atomic.Uint64` to plain `uint64` under the listener's **already existing** mutex (`internal/netlimit/listener.go:127-152`), for a reason that is stated there and is specific: `Stats()` must be a **consistent five-field snapshot** because its fields carry a cross-field invariant (`MaxPerSource <= LiveTotal <= MaxTotal`, asserted by `TestStats_IsOneCriticalSection`), and plain fields make an unsynchronised access a data race `-race` can see. **Neither condition holds here.** `ingestLogLimiter` has **no mutex, by explicit design** (`ingest_log_limiter.go:72-77`), the item's standing constraint forbids adding one to the recv goroutine (item line 162), and the ten counts have **no cross-field invariant whatsoever** - each is an independent monotonic total, so a torn snapshot means only "these two numbers were read microseconds apart", which is already true of any two counters read across an HTTP request. **Decision: `atomic.Uint64`. The reason goes in the code, next to a sentence saying why `netlimit` did the opposite**, so the next reader does not "fix" the inconsistency.
- **R5. REFUTED - the counters do NOT count every silent drop, and the payload must not say they do.** `handler.go:774` reads `if !errors.Is(err, pgx.ErrNoRows) && lim.allow(...)`. The `&&` short-circuits, so a `GetTask` `ErrNoRows` **never reaches `allow`** and is never counted - correctly, because the decision not to log was made upstream of the budget. Likewise the fence-rejection arm (`handler.go:1084-1111`) never calls `allow` at all; that is slice 3's counter. `deduped` and `suppressed` mean **"the log budget dropped a line"**, never "a diagnostic was lost". Anything vaguer is wrong prose about correct code, the defect class this project has led with for twelve consecutive iterations.
- **R6. REFUTED - "it is one array, one pointer, two increments and one comment amendment" (item lines 222-225).** Missing from that list, all verified: the sentinel and its two guards; the `Handler` field and its `go vet` copylocks consequence; **four** `newIngestLogLimiter*` call sites, two of them in `//go:build integration` `export_test.go:56,88`; two existing unit-test call sites (`ingest_log_limiter_test.go:50,58`); the api section plus edits to **two shipped payload guards**; the `httpServerDeps` field; the wiring-guard generalization; and README. This is a full slice.
- **R7. REFUTED (a hidden edit) - `TestCounterPayloadBytesCarryNoIdentifiers` MUST be modified.** Its comment (`server_counters_test.go:628-632`) says it wires **every** section; its final `ElementsMatch` compares against `counterPayloadLeaves`. Adding ten leaves without wiring the new source in that test turns a shipped test RED. **This is a required, legitimate edit - do not report it as an unexpected break, and do not weaken the assertion.**
- **R8. CHECKED, not assumed - a counts-only section (no `levels`) is tolerated by both guards.** The type walk recurses on struct fields generically (`server_counters_test.go:556-620`); the bytes walk recurses on `map[string]any` and only requires each object be non-empty (`:660-669`). Neither requires a `levels` key. **The one test that requires `levels` is scoped to `grpc_admission`** (`:108-125`), so it does not break - but note its per-half loop asserts every field serialises as the scalar `"0"` (`:118-125`), which is **false for this section**, whose `counts` half contains two nested objects. The analogous new test needs its own two-level walk; copying the existing one would fail. There are no candidate `levels` for this section: every limiter is a per-connection stack local, so a process-wide "current" figure would require enumerating live limiters, i.e. exactly the shared registry the type's comment (`ingest_log_limiter.go:72-77`) exists to refuse.
- **R9. REFUTED - the item's "handler-layer test that drives a flood" does NOT need Docker.** `handleTaskLog`'s bad-id arm returns at `handler.go:1058`, **before** `h.q` is touched; `handleTaskStatus`'s at `:746`, before `h.q.GetTask`. So the acceptance test drives a real flood through the real handler with a bare `&Handler{}` and no database, **in the default lane CI runs** (`.github/workflows/go-ci.yml:34` runs `go test -race ./...` with no build tag and no container). This is the slice-1 retro's build-tag lesson applied upstream. Only the Connect-level aggregation test genuinely needs Postgres, because `Connect` reaches `authenticateAndRegister` and the store.
- **R10. UPGRADED, not refuted - "the `l == nil` arm must NOT be counted" becomes "cannot be counted".** Because the counters are reached **through the limiter** (`l.drops`), a nil receiver has no counter to reach. Counting the nil arm would require adding a package-level fallback, which is a visible, arguable diff rather than an accident. Make it unwritable, then say so.

### One prose defect this slice will create if nobody edits it

`handler.go:217-228` currently ends: *"It never escapes this goroutine, which is what lets it be mutex-free. DO NOT capture it in a goroutine, store it anywhere, or hand it to anything that outlives this call."* After Task 3 the limiter still never escapes, **but it now holds a pointer to state shared with every other connection**. Left alone, that paragraph becomes wrong prose about correct code. Task 3 amends it with exact text below.

---

## File structure

**Create:**

- `internal/worker/ingest_log_counters.go` - the counters array, the two arm constants, `record`, `snapshot`, and the two exported value types. One responsibility: counting and publishing ingest-log-budget drops. Kept out of `ingest_log_limiter.go` so the limiter file stays about the limiter.
- `internal/worker/ingest_log_counters_test.go` - `package worker`, **no build tag** (default lane): the kind guards, the counters unit tests, the arm tests, the handler-layer flood tests, the concurrency test.

**Modify:**

- `internal/worker/ingest_log_limiter.go` - const block gains `kindCount` (`:99-105`); the `logKind` doc comment is rewritten (`:87-89`); `ingestLogLimiter` gains a `drops` field (`:78-85`); both constructors take it (`:179-194`); the two suppression arms record (`:258-267`).
- `internal/worker/handler.go` - `Handler` gains the counters field and the exported snapshot method (`:140-170`); `Connect`'s allocation site and its comment (`:217-228`).
- `internal/worker/ingest_log_limiter_test.go` - two call sites only (`:50`, `:58`).
- `internal/worker/export_test.go` (`//go:build integration`) - two call sites (`:56`, `:88`); `NewLimiterForTest` becomes a method on `*Handler` (it has **no callers**; verified by grep across the tree).
- `internal/worker/handler_ingest_budget_integration_test.go` - one new test.
- `internal/api/server_counters.go` - the source interface, the `CounterSources` field, the section types, the handler mapping, doc-comment additions.
- `internal/api/server_counters_test.go` - a fake source, three new tests, ten new `counterPayloadLeaves` entries, and the required edit to `TestCounterPayloadBytesCarryNoIdentifiers`.
- `cmd/relay-server/http_server.go` - `httpServerDeps.agentHandler`, the typed-nil filter, doc comment.
- `cmd/relay-server/main.go` - one field at the `buildHTTPServer` call site (`:215-230`).
- `cmd/relay-server/counters_wiring_test.go` - two new executed tests; `TestServerCountersIsWiredByMain` generalized from one wired dependency to a table of two, plus the new same-object check.
- `README.md` - the Server counters subsection (`:1254-1277`).

**Critical files to read before starting:** `internal/worker/ingest_log_limiter.go` in full, `internal/api/server_counters.go` in full, `internal/api/server_counters_test.go:453-523`, `cmd/relay-server/http_server.go` in full, `cmd/relay-server/counters_wiring_test.go:182-395`, and `docs/retros/2026-08-21-silent-drop-observability-slice1.md` (the guard escalation ladder).

---

## Standing constraints for every task

1. **No new log line anywhere on the ingest path.** A counter, never a `log.Printf`. That sentence goes in the code (item line 146).
2. **No new DB round trip, goroutine, queue or lock on the recv goroutine.** An atomic add is not a lock.
3. **No SQL, no `.sql` file, no `make generate`, no migration, no proto.** If a step seems to need one, the step is wrong (spec section 13).
4. **Never panic on the recv goroutine.** `Connect` has no recover and grpc-go does not recover handler panics; a panic kills the process.
5. **Do not weaken any existing assertion.** The only permitted edits to shipped tests are the ones named in R7 and Task 3's two call sites. Anything else that turns red is a **finding to report, not to fix**.

---

### Task 1: The kind sentinel and its two guards

**Files:**
- Modify: `internal/worker/ingest_log_limiter.go:87-105`
- Create: `internal/worker/ingest_log_counters_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/worker/ingest_log_counters_test.go`:

```go
package worker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIngestLogKindsAreADenseRunFromOne pins the property ingestLogCounters'
// array depends on: the kinds are 1, 2, 3, ... with kindCount immediately after
// the last one.
//
// This is the LAST rung of the guard ladder (match a shape) and it is used here
// because the property is one the compiler cannot express. It is load-bearing
// anyway: ingestLogCounters.record fails CLOSED on an out-of-range kind rather
// than panicking, because a panic on the gRPC recv goroutine kills the process -
// so a sparse or renumbered kind is a SILENT loss of that kind's counts, which
// is the exact defect this whole slice exists to close.
func TestIngestLogKindsAreADenseRunFromOne(t *testing.T) {
	run := []logKind{
		kindTaskLogPersist,
		kindBadTaskIDLog,
		kindBadTaskIDStatus,
		kindStatusGetTask,
		kindInventory,
	}
	for i, k := range run {
		require.Equal(t, logKind(i+1), k,
			"kind #%d is %d. The kinds index ingestLogCounters' array, so they must stay a DENSE RUN "+
				"starting at 1: a gap makes record() drop that kind's counts silently.", i, k)
	}
	require.Equal(t, logKind(len(run)+1), kindCount,
		"kindCount must be the sentinel immediately after the last kind: it is the LENGTH of the "+
			"counters array, so a kind at or beyond it is never counted")
}

// TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished counts a PROPERTY
// rather than matching a spelling: every `kind:` expression in every logKey
// composite literal in the package's non-test sources must be one of the kind
// constants declared inside the sentinel.
//
// It parses the PACKAGE, not one file, and it resolves const types the way Go
// does (a ConstSpec with no type and no values inherits the previous spec's
// type), so these evasions are all RED:
//
//   - a sixth kind declared in a SEPARATE const block;
//   - a sixth kind declared in a SIBLING FILE of the same package;
//   - a sixth kind declared AFTER kindCount (the dense-run test above cannot
//     see that one: kindCount stays kindInventory+1 and everything still lines
//     up);
//   - `logKey{kind: someLocalVariable}`, which is not a counted constant and so
//     fails closed rather than being skipped.
//
// THE KNOWN RESIDUAL, stated so the next reader does not assume it is covered:
// an UNTYPED `kindFoo = 9` in the same block is not a logKind constant to this
// walk, so declaring one and using it fails here (good) but for the "not a
// counted constant" reason rather than the "outside the sentinel" reason. The
// failure message says both.
func TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	declared := map[string]bool{}
	var literalKinds []string
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				typ := ""
				for _, s := range gd.Specs {
					vs, ok := s.(*ast.ValueSpec)
					if !ok {
						continue
					}
					switch {
					case vs.Type != nil:
						typ = ""
						if id, ok := vs.Type.(*ast.Ident); ok {
							typ = id.Name
						}
					case len(vs.Values) > 0:
						// A fresh expression list takes its type from the
						// expression, NOT from the previous spec.
						typ = ""
					}
					if typ != "logKind" {
						continue
					}
					for _, n := range vs.Names {
						declared[n.Name] = true
					}
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				cl, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				if id, ok := cl.Type.(*ast.Ident); !ok || id.Name != "logKey" {
					return true
				}
				for _, e := range cl.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "kind" {
						continue
					}
					id, ok := kv.Value.(*ast.Ident)
					require.True(t, ok,
						"a logKey literal names its kind as %T. It must name one of the logKind "+
							"constants directly, or this guard cannot tell whether that kind is counted.",
						kv.Value)
					literalKinds = append(literalKinds, id.Name)
				}
				return true
			})
		}
	}

	require.Equal(t, int(kindCount), len(declared),
		"the package declares %d logKind constants but kindCount is %d. Every kind must be declared "+
			"INSIDE the sentinel run: one declared after kindCount, or with an explicit out-of-run "+
			"value, is never counted and never published, and record() drops it in silence.",
		len(declared), int(kindCount))
	require.NotEmpty(t, literalKinds, "this walk found no logKey literals at all, so it proved nothing")
	for _, name := range literalKinds {
		require.True(t, declared[name],
			"logKey{kind: %s} names something that is not a logKind constant declared inside the "+
				"sentinel run. Its drops are counted into no cell and published under no JSON key.", name)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/worker/ -run 'TestIngestLogKinds|TestEveryIngestLogKind' -v`
Expected: FAIL to build - `undefined: kindCount`. **This is a VACUOUS red** (a missing symbol, not a behaviour). It is accepted here because the subject of the task *is* the symbol; the behavioural discrimination comes from the mutation battery rows M9 and M10, which must be run in Task 9.

- [ ] **Step 3: Add the sentinel and rewrite the type comment**

In `internal/worker/ingest_log_limiter.go`, replace lines 87-89:

```go
// logKind partitions the budget's dedupe keys.
//
// TWO PROPERTIES OF THESE CONSTANTS ARE LOAD-BEARING, and the sentence this
// paragraph replaces - "Values are never persisted or sent anywhere, so they may
// be renumbered freely" - became false in BOTH directions on 2026-08-21, when
// the drops started being counted and published.
//
//   - THE VALUES ARE ARRAY INDICES. ingestLogCounters is a [kindCount][2] array
//     indexed by these constants, so they must stay a DENSE RUN starting at 1
//     with kindCount immediately after the last one. A gap, an explicit
//     out-of-run value, or a kind declared after the sentinel makes
//     ingestLogCounters.record drop that kind's counts SILENTLY - it fails
//     closed rather than panicking, because a panic on the recv goroutine kills
//     the process. Pinned by TestIngestLogKindsAreADenseRunFromOne and
//     TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished, which are the
//     only things keeping that branch unreachable.
//   - THE NAMES ARE A RESPONSE CONTRACT. GET /v1/server/counters publishes one
//     JSON key per kind under ingest_log_budget.counts.deduped and
//     .counts.suppressed. RENAMING A KIND RENAMES AN OPERATOR-VISIBLE KEY and a
//     field of worker.IngestLogDropsByKind; ADDING one that nothing publishes is
//     a hole with no error. Pinned by
//     TestIngestLogCounters_EveryKindIsPublishedDistinctly here and by
//     counterPayloadLeaves in internal/api.
type logKind uint8
```

Then append the sentinel to the const block, after `kindInventory` (currently `:104`):

```go
	kindInventory                          // handleInventoryUpdate's persist failure

	// kindCount MUST STAY LAST and is NOT a kind. It is the length of
	// ingestLogCounters' array. A kind added after it is not counted at all;
	// TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished is what makes
	// that a RED test rather than a silent hole.
	kindCount
)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/worker/ -run 'TestIngestLogKinds|TestEveryIngestLogKind' -v`
Expected: PASS, both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/ingest_log_limiter.go internal/worker/ingest_log_counters_test.go
git commit -m "feat(worker): add a kindCount sentinel and pin the logKind run"
```

---

### Task 2: The counters array and its snapshot

**Files:**
- Create: `internal/worker/ingest_log_counters.go`
- Modify: `internal/worker/ingest_log_counters_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/worker/ingest_log_counters_test.go`:

```go
// kindFieldValues returns the by-kind struct's fields by name, so the mapping
// test below asserts on a SET of values rather than on five hand-written
// equalities that a crossed assignment could satisfy in pairs.
func kindFieldValues(t *testing.T, v IngestLogDropsByKind) []uint64 {
	t.Helper()
	rv := reflect.ValueOf(v)
	out := make([]uint64, 0, rv.NumField())
	for i := 0; i < rv.NumField(); i++ {
		out = append(out, rv.Field(i).Uint())
	}
	return out
}

// TestIngestLogCounters_EveryKindIsPublishedDistinctly drives every (kind, arm)
// cell a DIFFERENT number of times and then requires the published struct to
// carry exactly those numbers, per arm.
//
// Distinct values per cell are what make this discriminating. Equal values would
// pass under a crossed field assignment, an off-by-one index, or a snapshot that
// read the same arm twice. Asserting the two arms SEPARATELY is what catches an
// arm swap: the combined multiset is identical either way.
func TestIngestLogCounters_EveryKindIsPublishedDistinctly(t *testing.T) {
	var c ingestLogCounters

	var wantDeduped, wantSuppressed []uint64
	n := uint64(1)
	for k := logKind(1); k < kindCount; k++ {
		for _, arm := range []int{ingestDropDeduped, ingestDropSuppressed} {
			for i := uint64(0); i < n; i++ {
				c.record(k, arm)
			}
			if arm == ingestDropDeduped {
				wantDeduped = append(wantDeduped, n)
			} else {
				wantSuppressed = append(wantSuppressed, n)
			}
			n++
		}
	}

	snap := c.snapshot()
	require.ElementsMatch(t, wantDeduped, kindFieldValues(t, snap.Deduped),
		"every kind must publish its OWN deduped cell. A missing value means a kind is counted but "+
			"never published; a duplicated value means two fields read one cell; a shifted set means "+
			"the array is indexed off by one.")
	require.ElementsMatch(t, wantSuppressed, kindFieldValues(t, snap.Suppressed),
		"the suppressed half must read the suppressed arm. Swapping the two arms leaves the COMBINED "+
			"multiset unchanged, which is why this assertion is per half.")
	require.Len(t, wantDeduped, reflect.TypeOf(IngestLogDropsByKind{}).NumField(),
		"there are %d kinds inside the sentinel and %d published fields. A kind with no field is "+
			"counted into a cell nobody reads.", len(wantDeduped),
		reflect.TypeOf(IngestLogDropsByKind{}).NumField())
}

// TestIngestLogCounters_AnOutOfRangeKindIsDroppedNotPanicked. record's bounds
// check exists because the alternative on the gRPC recv goroutine is a panic
// that kills the process (Connect has no recover; grpc-go does not recover
// handler panics). It is unreachable while the two kind guards above are green,
// and this test exists so that "unreachable" does not mean "untested".
func TestIngestLogCounters_AnOutOfRangeKindIsDroppedNotPanicked(t *testing.T) {
	var c ingestLogCounters
	require.NotPanics(t, func() {
		c.record(logKind(0), ingestDropDeduped)
		c.record(kindCount, ingestDropDeduped)
		c.record(logKind(200), ingestDropSuppressed)
		c.record(kindInventory, 7)
		c.record(kindInventory, -1)
	})
	require.Equal(t, IngestLogDrops{}, c.snapshot(),
		"an out-of-range kind or arm must be dropped, not folded into some other cell")
}
```

Add `"reflect"` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/worker/ -run TestIngestLogCounters -v`
Expected: FAIL to build - `undefined: ingestLogCounters`. Vacuous red; Step 3 converts it into a behavioural one.

- [ ] **Step 3: Write the type with a DELIBERATELY INERT `record`, and re-run**

This is the slice-1 technique for converting a vacuous red into a behavioural one: ship the HEAD behaviour (nothing is counted) behind the real symbols first.

Create `internal/worker/ingest_log_counters.go`:

```go
package worker

import "sync/atomic"

// The two suppression arms of ingestLogLimiter.allow, and only those two. They
// mean different things to an operator and must never be summed into one
// number: DEDUPED is a healthy repeating failure being collapsed, SUPPRESSED is
// either an attack or a misconfiguration.
//
// THERE IS NO THIRD ARM HERE ON PURPOSE. allow returns false in three places;
// the third is its `l == nil` fail-closed guard, where NO EVENT WAS SUPPRESSED
// because there was no limiter. Counting it would count a phantom. It is also
// unwritable from here: these counters are reached through the limiter, so a nil
// receiver has nothing to increment, and adding a package-level fallback to
// count it would be a visible diff rather than an accident.
const (
	ingestDropDeduped = iota
	ingestDropSuppressed
	ingestDropArms
)

// IngestLogDrops is a snapshot of what one server's ingest log budget has
// dropped since process start.
//
// WHAT THESE NUMBERS ARE. They count LOG LINES THE BUDGET DROPPED, not
// diagnostics lost. A handler that decides not to log without consulting the
// budget contributes nothing here - handleTaskStatus's pgx.ErrNoRows GetTask
// (which short-circuits before allow) and handleTaskLog's fence-rejection arm
// (whose counter is a separate item) are both invisible to these fields, by
// design.
//
// MONOTONIC, per process, zeroed by a restart, and never returned to an agent:
// the only read path is the admin-authenticated GET /v1/server/counters.
type IngestLogDrops struct {
	// Deduped is the healthy arm: the key was logged inside
	// ingestLogDedupeWindow, so this occurrence was folded into an earlier line.
	// A large number next to a small line count is a repeating failure being
	// collapsed exactly as intended.
	Deduped IngestLogDropsByKind

	// Suppressed is the loud arm: the key was new or re-armed and the
	// connection's token bucket was empty, so the line was dropped entirely.
	// Non-zero means some connection is producing distinct failures faster than
	// 6 lines per minute, which is either an attack or a misconfiguration.
	Suppressed IngestLogDropsByKind
}

// IngestLogDropsByKind splits an arm by which log site was dropped.
//
// A STRUCT, NOT A MAP, and the choice is load-bearing rather than stylistic. The
// kind set is closed at compile time, so a fixed set of named fields makes
// unbounded key cardinality structurally impossible, and it keeps
// internal/api's two payload walks at full reach: an entry on
// counterPayloadAllowList is shape-checked but NON-DESCENDING, so a map here
// would have to re-implement key, value and cardinality checking inside its own
// exemption predicates. See counterPayloadExemption's comment.
//
// THESE FIELD NAMES ARE PART OF A RESPONSE CONTRACT. Each maps to one JSON key
// under ingest_log_budget.counts; see the logKind block in
// ingest_log_limiter.go.
type IngestLogDropsByKind struct {
	TaskLogPersist  uint64
	BadTaskIDLog    uint64
	BadTaskIDStatus uint64
	StatusGetTask   uint64
	Inventory       uint64
}

// ingestLogCounters is the process-lifetime home for what the per-connection log
// budgets dropped. It is a VALUE FIELD on Handler, not a package-level var:
// there is exactly one Handler per server process (main registers one with
// RegisterAgentServiceServer), so per-Handler IS process-wide in production,
// while every test gets its own and no count leaks between them.
//
// ATOMICS, NOT A MUTEX, AND netlimit DELIBERATELY DID THE OPPOSITE. netlimit's
// refusal counters are plain uint64 under the listener's existing mutex, because
// its snapshot carries a cross-field invariant (max_per_source <= live_total <=
// the configured cap) that only one critical section can hold, and plain fields
// make an unsynchronised access a data race -race can see. NEITHER APPLIES HERE.
// These ten numbers have no relation to each other - each is an independent
// monotonic total - so a snapshot that reads them microseconds apart is not
// inconsistent, merely unsynchronised in a way nothing can observe. And the
// increment site is the gRPC recv goroutine, whose standing constraint is no new
// lock, queue, goroutine or round trip: an atomic add is one locked
// exchange-add, no allocation and no scheduling, which is what lets
// ingestLogLimiter keep its documented no-mutex property VERBATIM.
//
// COUNTERS, NEVER LOG LINES. The next person to "improve" one of these into a
// log.Printf hands back the exact vector
// bug-2026-08-12-tasklog-err-limiter-attacker-keyed closed. Do not.
type ingestLogCounters struct {
	n [kindCount][ingestDropArms]atomic.Uint64
}

// record adds one drop. Out of range fails CLOSED - see the comment on the
// bounds check - and the kind guards in ingest_log_counters_test.go are what
// keep that branch unreachable.
func (c *ingestLogCounters) record(k logKind, arm int) {
	// STUB: no counting yet. Replaced in the next step. Present so the tests
	// above fail on a NUMBER rather than on a missing symbol.
	_ = k
	_ = arm
}

func (c *ingestLogCounters) snapshot() IngestLogDrops {
	return IngestLogDrops{
		Deduped:    c.byKind(ingestDropDeduped),
		Suppressed: c.byKind(ingestDropSuppressed),
	}
}

// byKind reads one arm. Every field here is one JSON key of the endpoint's
// ingest_log_budget section; adding a kind without adding a line here counts it
// into a cell nobody reads, which
// TestIngestLogCounters_EveryKindIsPublishedDistinctly turns RED.
func (c *ingestLogCounters) byKind(arm int) IngestLogDropsByKind {
	return IngestLogDropsByKind{
		TaskLogPersist:  c.n[kindTaskLogPersist][arm].Load(),
		BadTaskIDLog:    c.n[kindBadTaskIDLog][arm].Load(),
		BadTaskIDStatus: c.n[kindBadTaskIDStatus][arm].Load(),
		StatusGetTask:   c.n[kindStatusGetTask][arm].Load(),
		Inventory:       c.n[kindInventory][arm].Load(),
	}
}
```

Run: `go test ./internal/worker/ -run TestIngestLogCounters -v`
Expected: `TestIngestLogCounters_EveryKindIsPublishedDistinctly` FAILS behaviourally - `ElementsMatch` reports `[]uint64{0,0,0,0,0}` against `[1 3 5 7 9]`. `TestIngestLogCounters_AnOutOfRangeKindIsDroppedNotPanicked` PASSES (vacuously, and it will still pass after Step 4 - it is a mutation guard, not a red).

- [ ] **Step 4: Implement `record`**

Replace the stub body:

```go
func (c *ingestLogCounters) record(k logKind, arm int) {
	// FAIL CLOSED, DO NOT PANIC. An out-of-range index here would panic on the
	// gRPC recv goroutine, which Connect does not recover and grpc-go does not
	// recover either, so it would kill the whole server process. Losing a count
	// is the cheaper failure. This branch is UNREACHABLE while
	// TestIngestLogKindsAreADenseRunFromOne and
	// TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished are green, and
	// THOSE TWO TESTS ARE THE ONLY THING KEEPING IT SO - a reader who believes
	// the branch is dead has no reason to preserve them, which is precisely how
	// the property gets lost.
	i := int(k)
	if i <= 0 || i >= len(c.n) || arm < 0 || arm >= ingestDropArms {
		return
	}
	c.n[i][arm].Add(1)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/worker/ -run TestIngestLog -v`
Expected: PASS, all four tests from Tasks 1 and 2.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/ingest_log_counters.go internal/worker/ingest_log_counters_test.go
git commit -m "feat(worker): add per-kind, per-arm ingest log drop counters"
```

---

### Task 3: Thread the counters into the limiter and count the two arms

**Files:**
- Modify: `internal/worker/ingest_log_limiter.go:78-85`, `:179-194`, `:218-267`
- Modify: `internal/worker/handler.go:140-170`, `:217-228`
- Modify: `internal/worker/ingest_log_limiter_test.go:50`, `:58`
- Modify: `internal/worker/export_test.go:51-60`, `:85-89`
- Modify: `internal/worker/ingest_log_counters_test.go`

- [ ] **Step 1: Thread the pointer, WITHOUT counting anything yet**

`ingest_log_limiter.go`, in the `ingestLogLimiter` struct (after `now`):

```go
	now    func() time.Time // injectable for the deterministic refill tests only

	// drops is the process-lifetime counter home, shared with every other
	// connection's limiter. IT IS THE ONE THING IN THIS TYPE THAT IS SHARED, and
	// that is deliberate: a count of what was dropped has to outlive the
	// connection that caused it, or an operator reads zero after the attacker
	// disconnects. Every write to it is an atomic add - see ingestLogCounters -
	// so this type keeps its no-mutex property verbatim.
	drops *ingestLogCounters
```

Constructors:

```go
func newIngestLogLimiter(drops *ingestLogCounters) *ingestLogLimiter {
	return newIngestLogLimiterAt(time.Now, drops)
}

// newIngestLogLimiterAt is newIngestLogLimiter with the clock injected. [...keep
// the existing paragraph verbatim...]
func newIngestLogLimiterAt(now func() time.Time, drops *ingestLogCounters) *ingestLogLimiter {
	return &ingestLogLimiter{
		seen:   make(map[logKey]time.Time),
		tokens: ingestLogBurst,
		last:   now(),
		now:    now,
		drops:  drops,
	}
}
```

`handler.go`, in the `Handler` struct after `TrailingLogWindow`:

```go
	// ingestDrops counts what this server's per-connection log budgets dropped,
	// split by kind and by arm. A VALUE, not a pointer: the zero value is ready
	// to use, so a Handler built by any route (including a bare &Handler{} in a
	// test) has working counters and there is no nil case anywhere. Read through
	// IngestLogDropCounts; wired to GET /v1/server/counters by
	// cmd/relay-server's buildHTTPServer.
	//
	// It contains atomics, which makes Handler non-copyable - go vet's copylocks
	// check will say so at any `*h` copy. That is a feature: nothing should ever
	// copy a Handler.
	ingestDrops ingestLogCounters
```

and the exported accessor, next to it:

```go
// IngestLogDropCounts reports what this server's ingest log budget has dropped
// since process start, split by kind and by arm.
//
// It satisfies api.IngestLogBudgetSource. The numbers are per PROCESS - there is
// one Handler per server - and are never sent to an agent: the only read path is
// the admin-authenticated GET /v1/server/counters.
func (h *Handler) IngestLogDropCounts() IngestLogDrops { return h.ingestDrops.snapshot() }
```

`handler.go:228`, the allocation site, and its comment. Replace the final paragraph of the comment block (`:224-227`) and the call:

```go
	// It never escapes this goroutine, which is what lets it be mutex-free. DO
	// NOT capture it in a goroutine, store it anywhere, or hand it to anything
	// that outlives this call. TestConnect_TwoConnectionsDoNotShareTheLogBudget
	// is what pins this allocation site.
	//
	// ONE THING DOES POINT OUT OF THIS FRAME, and it is not the budget: the
	// limiter carries a pointer to the Handler's drop COUNTERS, which are shared
	// by every connection on purpose, because a count that died with the
	// connection would read zero exactly when an operator went looking for it.
	// The budget stays private; the counters are process-wide and atomic. Do not
	// merge the two.
	lim := newIngestLogLimiter(&h.ingestDrops)
```

Existing test call sites - `ingest_log_limiter_test.go:50`:

```go
	return newIngestLogLimiterAt(func() time.Time { return *clock }, &ingestLogCounters{}), clock
```

and `:58`:

```go
	l := newIngestLogLimiterAt(func() time.Time { return epoch }, &ingestLogCounters{})
```

`export_test.go:56`:

```go
		l = newIngestLogLimiter(&h.ingestDrops)
```

`export_test.go:85-89` - `NewLimiterForTest` has **no callers anywhere in the tree** (verified by grep), so give it the Handler it now needs:

```go
// NewLimiterForTest returns a fresh per-connection log budget, in the state
// Connect allocates one in, reporting its drops into this Handler's counters
// exactly as a real connection's does.
func (h *Handler) NewLimiterForTest() *LimiterHandle {
	return &LimiterHandle{l: newIngestLogLimiter(&h.ingestDrops)}
}
```

Run: `go build ./... && go vet ./... && make vet-integration`
Expected: all clean. If `go vet` reports a copylocks failure on a `Handler` copy, that copy is a **finding to report** - do not work around it by making the field a pointer without saying so.

- [ ] **Step 2: Write the failing arm tests**

Append to `internal/worker/ingest_log_counters_test.go`:

```go
// TestIngestLogLimiter_TheDedupeArmCountsDeduped. One key logged twice inside
// the dedupe window: the second call is folded into the first, and that is the
// arm an operator reads as "a repeating failure is being collapsed".
func TestIngestLogLimiter_TheDedupeArmCountsDeduped(t *testing.T) {
	l, _ := newFrozen()
	k := logKey{kind: kindTaskLogPersist, id: "task-a", epoch: 1}

	require.True(t, l.allow(k), "fixture: the first line is allowed")
	require.False(t, l.allow(k), "fixture: the second is deduped")

	got := l.drops.snapshot()
	require.Equal(t, uint64(1), got.Deduped.TaskLogPersist,
		"a deduped occurrence must increment the DEDUPED arm of its own kind")
	require.Equal(t, IngestLogDropsByKind{}, got.Suppressed,
		"a deduped occurrence must not touch the suppressed arm: the two mean different things - "+
			"one is a healthy collapse, the other is an attack or a misconfiguration")
}

// TestIngestLogLimiter_TheEmptyBucketCountsSuppressed. Distinct keys, so the
// dedupe arm never fires: the burst is spent and every later line is dropped
// entirely.
func TestIngestLogLimiter_TheEmptyBucketCountsSuppressed(t *testing.T) {
	l, _ := newFrozen()
	key := func(i int) logKey {
		return logKey{kind: kindTaskLogPersist, id: "task", epoch: int64(i)}
	}
	for i := 0; i < ingestLogBurst; i++ {
		require.True(t, l.allow(key(i)), "fixture: the burst must be spendable")
	}
	require.False(t, l.allow(key(ingestLogBurst)), "fixture: the bucket is now empty")
	require.False(t, l.allow(key(ingestLogBurst+1)))

	got := l.drops.snapshot()
	require.Equal(t, uint64(2), got.Suppressed.TaskLogPersist,
		"a line dropped for lack of a token must increment the SUPPRESSED arm")
	require.Equal(t, IngestLogDropsByKind{}, got.Deduped,
		"none of these keys repeated, so nothing was deduped")
}

// TestIngestLogLimiter_AnAllowedLineCountsNothing. The counter is a DROP
// counter. An increment on the allowed path would make every number here the
// message count, which is the one thing the log line already tells you.
func TestIngestLogLimiter_AnAllowedLineCountsNothing(t *testing.T) {
	l, _ := newFrozen()
	for k := logKind(1); k < kindCount; k++ {
		require.True(t, l.allow(logKey{kind: k}), "fixture: first line of each kind is allowed")
	}
	require.Equal(t, IngestLogDrops{}, l.drops.snapshot(),
		"an ALLOWED line is not a drop")
}

// TestIngestLogLimiter_TheNilArmCountsNothing.
//
// SAY WHAT THIS DOES AND DOES NOT BUY. The nil arm is fail-closed and
// deliberately unreachable in production, and NO EVENT WAS SUPPRESSED there
// because there was no limiter - counting it would count a phantom. What
// actually prevents the count is structural: the counters are reached through
// the limiter, so a nil receiver has nothing to increment. This test therefore
// kills exactly one mutation - adding a package-level fallback counter that the
// snapshot also reads - and NOT the general claim. Keep it for that one.
func TestIngestLogLimiter_TheNilArmCountsNothing(t *testing.T) {
	var h Handler
	var l *ingestLogLimiter
	require.False(t, l.allow(logKey{kind: kindBadTaskIDLog}))
	require.Equal(t, IngestLogDrops{}, h.IngestLogDropCounts(),
		"the l == nil arm suppressed no event and must count nothing anywhere")
}

// TestIngestLogCounters_TwoLimitersOnOneHandlerAggregate is the item's headline
// property at the cheapest layer that can express it: THE COUNT OUTLIVES THE
// CONNECTION. Two independent budgets, one Handler, one set of numbers.
//
// It does NOT prove that Connect passes the Handler's counters rather than a
// fresh set - that needs a real stream and lives in
// TestConnect_IngestDropCountsSurviveAndAggregateAcrossConnections in the
// integration lane.
func TestIngestLogCounters_TwoLimitersOnOneHandlerAggregate(t *testing.T) {
	var h Handler
	a := newIngestLogLimiter(&h.ingestDrops)
	b := newIngestLogLimiter(&h.ingestDrops)
	k := logKey{kind: kindInventory}

	for _, l := range []*ingestLogLimiter{a, b} {
		require.True(t, l.allow(k))
		require.False(t, l.allow(k))
		require.False(t, l.allow(k))
	}

	require.Equal(t, uint64(4), h.IngestLogDropCounts().Deduped.Inventory,
		"two connections' drops must land in ONE process-lifetime counter. Per-connection "+
			"accumulation flushed at teardown was refuted at spec time: it reports nothing at all "+
			"for as long as the flood continues.")
}

// TestIngestLogCounters_TwoHandlersDoNotShareCounts pins the choice of home
// against the one the spec proposed (a package-level array). A global would make
// every exact-count assertion in this package order-dependent on every other
// test in the binary.
func TestIngestLogCounters_TwoHandlersDoNotShareCounts(t *testing.T) {
	var a, b Handler
	l := newIngestLogLimiter(&a.ingestDrops)
	k := logKey{kind: kindStatusGetTask}
	require.True(t, l.allow(k))
	require.False(t, l.allow(k))

	require.Equal(t, uint64(1), a.IngestLogDropCounts().Deduped.StatusGetTask)
	require.Equal(t, IngestLogDrops{}, b.IngestLogDropCounts(),
		"counters are per Handler. Production has exactly one Handler, so that is process-wide "+
			"there; a package-level array would make every test in this binary share these numbers.")
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/worker/ -run 'TestIngestLogLimiter_The|TestIngestLogCounters_Two' -v`
Expected: **behavioural** FAIL on four of the five - `Deduped.TaskLogPersist` is `0`, want `1`; `Suppressed.TaskLogPersist` is `0`, want `2`; the aggregate is `0`, want `4`; the two-handler test's first assertion is `0`, want `1`. `TestIngestLogLimiter_AnAllowedLineCountsNothing` and `..._TheNilArmCountsNothing` PASS already - they are mutation guards, and the battery is where they earn their place.

- [ ] **Step 4: Count the two arms**

`ingest_log_limiter.go`, the dedupe arm (currently `:258-260`):

```go
	if at, ok := l.seen[k]; ok && now.Sub(at) < ingestLogDedupeWindow {
		// COUNTED, NOT LOGGED. A log line here would be caller-driven volume on
		// the recv goroutine, which is the vector this whole type closes.
		l.drops.record(k.kind, ingestDropDeduped)
		return false
	}
```

the spend arm (currently `:265-267`):

```go
	if l.tokens == 0 {
		// The other arm, kept separate because they mean opposite things: this
		// one is a line dropped ENTIRELY, and a non-zero number here is either
		// an attack or a misconfiguration, while a deduped line is a healthy
		// collapse. One number for both would be uninterpretable.
		l.drops.record(k.kind, ingestDropSuppressed)
		return false
	}
```

Leave the `l == nil` arm (`:223-225`) untouched, and extend its comment with one sentence:

```go
	// Fail CLOSED rather than panic. [...existing text...] Production has
	// exactly one allocation site (Connect) so this is unreachable there.
	//
	// NOTHING IS COUNTED HERE, and it is not an oversight: no event was
	// suppressed on this path, because there was no limiter. It is also
	// unreachable to count - the counters live behind l - and adding a
	// package-level fallback to reach them would be a visible diff, not an
	// accident.
	if l == nil {
		return false
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/worker/ -v`
Expected: PASS, whole package (default lane). The pre-existing limiter tests must be unchanged in result; if any is not, that is a finding.

- [ ] **Step 6: Commit**

```bash
git add internal/worker/
git commit -m "feat(worker): count both ingest log budget suppression arms"
```

---

### Task 4: The acceptance test - a real handler-layer flood, in the default lane

**Files:**
- Modify: `internal/worker/ingest_log_counters_test.go`

This is the item's first Done-When bullet ("proven by a handler-layer test that drives a flood and reads the counters"). It runs with **no database and no build tag**, because `handleTaskLog` returns at `handler.go:1058` before `h.q` is touched - the slice-1 retro's lesson applied before the mistake instead of after it.

- [ ] **Step 1: Write the test**

Append to `internal/worker/ingest_log_counters_test.go`:

```go
// captureUnitLog redirects the standard logger for one test. The package's
// integration lane has its own captureLog in package worker_test; this is the
// default-lane twin. No test in this package calls t.Parallel, which is what
// makes a process-global redirect safe here.
func captureUnitLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })
	return buf.String
}

// TestHandleTaskLog_ABadTaskIDFloodCountsTheDedupedArm drives the item's own
// Repro through the REAL handler: one connection, a flood of chunks whose task
// id does not parse. The operator-visible signature is 1 log line and 99 silent
// drops, and until this slice nothing anywhere said so.
//
// NO DATABASE AND NO BUILD TAG. handleTaskLog's bad-id arm returns before h.q is
// touched, so a bare &Handler{} is a complete fixture and this proof runs in the
// lane CI actually executes (go-ci runs `go test -race ./...` with no tag).
func TestHandleTaskLog_ABadTaskIDFloodCountsTheDedupedArm(t *testing.T) {
	h := &Handler{}
	lim := newIngestLogLimiter(&h.ingestDrops)
	logged := captureUnitLog(t)

	const flood = 100
	for i := 0; i < flood; i++ {
		h.handleTaskLog(context.Background(), pgtype.UUID{}, lim, &relayv1.TaskLogChunk{
			TaskId:  "not-a-uuid",
			Content: []byte("x"),
		})
	}

	require.Equal(t, 1, strings.Count(logged(), "handleTaskLog bad task id"),
		"fixture: this kind carries no wire value, so the flood is ONE key and one line")

	got := h.IngestLogDropCounts()
	require.Equal(t, uint64(flood-1), got.Deduped.BadTaskIDLog,
		"the 99 chunks folded into that one line must be counted. That number is the whole point of "+
			"this slice: without it, a flood is indistinguishable from a healthy fleet.")
	require.Zero(t, got.Suppressed.BadTaskIDLog, "nothing was budget-suppressed: the key repeated")
	require.Zero(t, got.Deduped.TaskLogPersist, "the count must be attributed to the RIGHT kind")
}

// TestHandleTaskStatus_ADroppedLineUnderAnEmptyBudgetCountsSuppressed is the
// other arm at the handler layer, and it is the arm that matters under attack.
// The bucket is drained by a DIFFERENT kind first, which is the realistic shape:
// the budget is per connection and shared across all five kinds, so one flooding
// site silences the others.
func TestHandleTaskStatus_ADroppedLineUnderAnEmptyBudgetCountsSuppressed(t *testing.T) {
	h := &Handler{}
	lim := newIngestLogLimiter(&h.ingestDrops)
	for i := 0; i < ingestLogBurst; i++ {
		require.True(t, lim.allow(logKey{kind: kindTaskLogPersist, id: "t", epoch: int64(i)}),
			"fixture: drain the whole burst through another kind")
	}

	logged := captureUnitLog(t)
	h.handleTaskStatus(context.Background(), pgtype.UUID{}, lim, &relayv1.TaskStatusUpdate{
		TaskId: "also-not-a-uuid",
	})

	require.Equal(t, "", logged(),
		"fixture: with an empty bucket the line is dropped entirely")
	got := h.IngestLogDropCounts()
	require.Equal(t, uint64(1), got.Suppressed.BadTaskIDStatus,
		"a line dropped for lack of a token is the arm that means attack or misconfiguration, and it "+
			"must be counted under the kind that lost it")
	require.Zero(t, got.Deduped.BadTaskIDStatus)
}
```

Add `"bytes"`, `"context"`, `"log"`, `"strings"`, `relayv1 "relay/internal/proto/relayv1"` and `"github.com/jackc/pgx/v5/pgtype"` to the test file's imports.

- [ ] **Step 2: Run the tests - they will PASS, so prove RED by mutation**

Run: `go test ./internal/worker/ -run 'TestHandleTaskLog_ABadTaskID|TestHandleTaskStatus_ADropped' -v`
Expected: PASS. These are **acceptance** tests written after the mechanism, so their red must be produced deliberately:

1. Comment out `l.drops.record(k.kind, ingestDropDeduped)` in `ingest_log_limiter.go`. Re-run. Expected: FAIL - `Deduped.BadTaskIDLog` is `0`, want `99`. Restore.
2. Comment out `l.drops.record(k.kind, ingestDropSuppressed)`. Re-run. Expected: FAIL - `Suppressed.BadTaskIDStatus` is `0`, want `1`. Restore.
3. Change the suppressed arm's record to `ingestDropDeduped`. Re-run. Expected: FAIL. Restore.

Record all three observed outputs in the task report. A mutation proof must leave a test behind, and these are it.

- [ ] **Step 3: Run the whole package**

Run: `go test ./internal/worker/ -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/worker/ingest_log_counters_test.go
git commit -m "test(worker): handler-layer flood proves both drop arms are counted"
```

---

### Task 5: The concurrency proof

**Files:**
- Modify: `internal/worker/ingest_log_counters_test.go`

- [ ] **Step 1: Write the test**

```go
// TestIngestLogCounters_ConcurrentDropsFromManyLimitersAreExact is what makes
// "atomics, not a mutex" a checked decision rather than a comment. Every
// goroutine owns its own limiter, exactly as every connection does, and all of
// them write the same cells.
//
// WHAT KILLS WHAT, because a test can be robust and inert on the same machine:
//
//   - The EXACTNESS assertion kills a plain `uint64` increment through LOST
//     UPDATES, and that kill needs real parallelism - on one CPU the goroutines
//     may not interleave inside the read-modify-write at all.
//   - The -race run kills the same mutation through happens-before analysis,
//     which does NOT need real parallelism, so it is the load-bearing half on a
//     constrained box.
//
// go-ci runs `go test -race ./...` on ubuntu-latest with no -cpu flag (2-4
// vCPUs), so both halves are live there. Locally, -race must be run in a Linux
// container: ThreadSanitizer cannot allocate its shadow memory on the Windows
// authoring host and fails on untouched packages too.
func TestIngestLogCounters_ConcurrentDropsFromManyLimitersAreExact(t *testing.T) {
	var h Handler
	const conns, perConn = 8, 200
	k := logKey{kind: kindInventory}

	var wg sync.WaitGroup
	for c := 0; c < conns; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := newIngestLogLimiter(&h.ingestDrops)
			require.True(t, l.allow(k))
			for i := 0; i < perConn; i++ {
				l.allow(k)
			}
		}()
	}

	// A concurrent READER as well, so -race sees both sides of the access.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5000; i++ {
			_ = h.IngestLogDropCounts()
		}
	}()
	wg.Wait()
	<-done

	require.Equal(t, uint64(conns*perConn), h.IngestLogDropCounts().Deduped.Inventory,
		"every drop must land exactly once. A short count here is a lost update, which is what a "+
			"plain uint64 increment produces under concurrency.")
}
```

Add `"sync"` to the imports.

- [ ] **Step 2: Run it, and prove the mutation**

Run: `go test ./internal/worker/ -run TestIngestLogCounters_Concurrent -count=5 -v`
Expected: PASS 5/5.

Then, in an **isolated worktree** (never the shared tree - sibling agents read it):

```bash
git worktree add --detach C:/Users/chadv/AppData/Local/Temp/relay-mutate HEAD
```

In that copy, change `ingestLogCounters.n` to `[kindCount][ingestDropArms]uint64` with `c.n[i][arm]++` and `c.n[k][arm]` reads, then:

```
docker run --rm -v C:/Users/chadv/AppData/Local/Temp/relay-mutate:/src -w /src golang:1.26 \
  go test -race ./internal/worker/ -run TestIngestLogCounters_Concurrent -count=5
```

Expected: FAIL - `DATA RACE`. Also run it once with `-cpu=1` and record whether the exactness assertion alone fails there; report the number either way rather than assuming. Remove the mutation worktree afterwards.

- [ ] **Step 3: Commit**

```bash
git add internal/worker/ingest_log_counters_test.go
git commit -m "test(worker): concurrent drops from many limiters are exact and race-free"
```

---

### Task 6: The integration-lane proof that Connect wires the Handler's counters

**Files:**
- Modify: `internal/worker/handler_ingest_budget_integration_test.go`

**Why this one IS behind `//go:build integration`, checked rather than assumed:** `Connect` reaches `authenticateAndRegister`, which reads and writes the store, so it genuinely needs Postgres. Everything that does not need Postgres is in Task 4, in the default lane.

- [ ] **Step 1: Write the test**

Append to `internal/worker/handler_ingest_budget_integration_test.go`, directly after `TestConnect_TwoConnectionsDoNotShareTheLogBudget`:

```go
// TestConnect_IngestDropCountsSurviveAndAggregateAcrossConnections is the
// property the item exists for and the one no unit test can reach: the numbers
// OUTLIVE the connection that produced them, and they come from the Handler
// Connect was called on rather than from a set allocated per stream.
//
// It is the sibling of TestConnect_TwoConnectionsDoNotShareTheLogBudget above:
// that one proves the BUDGET is per connection, this one proves the COUNTERS are
// not. Both must hold at once, and that pair is the whole design.
//
// Per connection: 64 unpersistable inventory updates, one key (kindInventory
// carries no wire value), so 1 logged line and 63 deduped drops.
func TestConnect_IngestDropCountsSurviveAndAggregateAcrossConnections(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), events.NewBroker(), func() {})

	_, tokenA := seedWorkerWithAgentToken(t, ctx, q, "drops-a")
	_, tokenB := seedWorkerWithAgentToken(t, ctx, q, "drops-b")

	require.NoError(t, h.Connect(inventoryFloodStream(ctx, "drops-a", tokenA, 64)))
	afterA := h.IngestLogDropCounts()
	assert.Equal(t, uint64(63), afterA.Deduped.Inventory,
		"the counts must be readable AFTER the connection has ended. Accumulating in the limiter and "+
			"flushing at teardown was refuted at spec time precisely because it reports nothing while "+
			"the flood is happening; a count that died with the frame would report nothing at all.")

	require.NoError(t, h.Connect(inventoryFloodStream(ctx, "drops-b", tokenB, 64)))
	afterB := h.IngestLogDropCounts()
	assert.Equal(t, uint64(126), afterB.Deduped.Inventory,
		"a second connection's drops must ADD to the first's. A per-stream counter set would report "+
			"63 here, and the endpoint would then show one connection's worth of a fleet-wide flood.")
	assert.Zero(t, afterB.Suppressed.Inventory,
		"one repeating key costs one token per dedupe window, so nothing is budget-suppressed")
	assert.Zero(t, afterB.Deduped.TaskLogPersist, "attributed to the right kind")
}
```

- [ ] **Step 2: Run it**

Requires Docker Desktop running.
Run: `go test -tags integration -p 1 ./internal/worker/ -run 'TestConnect_IngestDropCounts' -v -timeout 300s`
Expected: PASS.

- [ ] **Step 3: Prove the mutation**

In the isolated mutation worktree, change `handler.go`'s allocation site to `lim := newIngestLogLimiter(&ingestLogCounters{})`. Re-run this test. Expected: FAIL - `afterA.Deduped.Inventory` is `0`, want `63`. Restore. Record the observed output.

- [ ] **Step 4: Commit**

```bash
git add internal/worker/handler_ingest_budget_integration_test.go
git commit -m "test(worker): drop counts survive the connection and aggregate across them"
```

---

### Task 7: The `ingest_log_budget` payload section

**Files:**
- Modify: `internal/api/server_counters.go`
- Modify: `internal/api/server_counters_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/server_counters_test.go` (and see Step 4 for the two required edits to existing code in that file):

```go
// fakeIngestLogSource returns a fixed snapshot. TEN DISTINCT VALUES: the mapping
// from worker.IngestLogDrops into the response types is ten hand-written
// assignments, and equal values would hide a crossed one.
type fakeIngestLogSource struct{ d worker.IngestLogDrops }

func (f fakeIngestLogSource) IngestLogDropCounts() worker.IngestLogDrops { return f.d }

func tenDistinctDrops() worker.IngestLogDrops {
	return worker.IngestLogDrops{
		Deduped: worker.IngestLogDropsByKind{
			TaskLogPersist: 11, BadTaskIDLog: 22, BadTaskIDStatus: 33,
			StatusGetTask: 44, Inventory: 55,
		},
		Suppressed: worker.IngestLogDropsByKind{
			TaskLogPersist: 66, BadTaskIDLog: 77, BadTaskIDStatus: 88,
			StatusGetTask: 99, Inventory: 110,
		},
	}
}

func TestServerCounters_ReportsTheIngestLogSnapshot(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{IngestLogBudget: fakeIngestLogSource{d: tenDistinctDrops()}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		IngestLogBudget *struct {
			Counts struct {
				Deduped    map[string]any `json:"deduped"`
				Suppressed map[string]any `json:"suppressed"`
			} `json:"counts"`
			Levels map[string]any `json:"levels"`
		} `json:"ingest_log_budget"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.IngestLogBudget, "a wired section must be present")
	require.Nil(t, body.Levels,
		"this section is COUNTS ONLY. Every limiter is a per-connection stack local, so there is no "+
			"process-wide 'current' figure to report without building the shared registry the limiter "+
			"deliberately refuses.")

	// Key-set equality, not per-key assertions alone: a renamed key would decode
	// as a missing value and a per-key check would report zero. THESE NAMES ARE A
	// RESPONSE CONTRACT - see the logKind block in internal/worker.
	kinds := []string{"task_log_persist", "bad_task_id_log", "bad_task_id_status", "status_get_task", "inventory"}
	assert.ElementsMatch(t, kinds, counterMapKeys(body.IngestLogBudget.Counts.Deduped))
	assert.ElementsMatch(t, kinds, counterMapKeys(body.IngestLogBudget.Counts.Suppressed))

	assert.Equal(t, float64(11), body.IngestLogBudget.Counts.Deduped["task_log_persist"])
	assert.Equal(t, float64(22), body.IngestLogBudget.Counts.Deduped["bad_task_id_log"])
	assert.Equal(t, float64(33), body.IngestLogBudget.Counts.Deduped["bad_task_id_status"])
	assert.Equal(t, float64(44), body.IngestLogBudget.Counts.Deduped["status_get_task"])
	assert.Equal(t, float64(55), body.IngestLogBudget.Counts.Deduped["inventory"])
	assert.Equal(t, float64(66), body.IngestLogBudget.Counts.Suppressed["task_log_persist"])
	assert.Equal(t, float64(77), body.IngestLogBudget.Counts.Suppressed["bad_task_id_log"])
	assert.Equal(t, float64(88), body.IngestLogBudget.Counts.Suppressed["bad_task_id_status"])
	assert.Equal(t, float64(99), body.IngestLogBudget.Counts.Suppressed["status_get_task"])
	assert.Equal(t, float64(110), body.IngestLogBudget.Counts.Suppressed["inventory"])
}

// TestServerCounters_WiredButZeroIngestSectionIsStillPresent is the
// absent-versus-zero contract for this section. A server whose budget has
// dropped nothing is the COMMON case, and it must not read as "this build does
// not have an ingest budget".
//
// It walks two levels rather than one: unlike grpc_admission, this section's
// counts half contains OBJECTS, so the shipped
// TestServerCounters_WiredButZeroSectionIsStillPresent's scalar loop would fail
// here. Do not copy that loop.
func TestServerCounters_WiredButZeroIngestSectionIsStillPresent(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{IngestLogBudget: fakeIngestLogSource{}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	require.ElementsMatch(t, []string{"started_at", "ingest_log_budget"}, counterKeys(top),
		"a WIRED source whose every counter is zero must still emit its section: zeros mean 'this "+
			"control ran and stopped nothing', absence means 'not wired on this build'")

	var section map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["ingest_log_budget"], &section))
	require.ElementsMatch(t, []string{"counts"}, counterKeys(section),
		"counts only; this section has no levels")

	var counts map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(section["counts"], &counts))
	require.ElementsMatch(t, []string{"deduped", "suppressed"}, counterKeys(counts))
	for _, arm := range []string{"deduped", "suppressed"} {
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(counts[arm], &fields))
		require.Len(t, fields, 5, "%s must carry one key per kind, not an empty object", arm)
		for k, v := range fields {
			assert.Equal(t, "0", string(v), "%s.%s must serialise as an explicit zero", arm, k)
		}
	}
}

// TestServerCounters_OneWiredSectionDoesNotDragInTheOther. Each section is its
// own nil-able source, so wiring the ingest budget must not conjure a
// grpc_admission object and vice versa.
func TestServerCounters_OneWiredSectionDoesNotDragInTheOther(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters:  CounterSources{IngestLogBudget: fakeIngestLogSource{d: tenDistinctDrops()}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	assert.ElementsMatch(t, []string{"started_at", "ingest_log_budget"}, counterKeys(top),
		"an unwired section stays ABSENT even when a sibling section is wired")
}
```

Add `"relay/internal/worker"` to the test file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/api/ -run TestServerCounters -v`
Expected: FAIL to build - `unknown field IngestLogBudget in struct literal`. Vacuous red; the discrimination is in the mutation battery (M12-M14).

- [ ] **Step 3: Implement the section**

In `internal/api/server_counters.go`, after `GRPCAdmissionSource`:

```go
// IngestLogBudgetSource is whatever can report what the per-connection agent log
// budgets have dropped - in production, *worker.Handler.
//
// ONE METHOD, AND A SEPARATE FIELD FROM ANY FUTURE WORKER COUNTER. The task-log
// fence-rejection counter (idea-2026-08-14) will live on the same *worker.Handler
// and must get its OWN source field and its own section, so that "wired" stays a
// per-SECTION fact. Widening this interface to carry both would make two
// independent controls appear and disappear together.
type IngestLogBudgetSource interface {
	IngestLogDropCounts() worker.IngestLogDrops
}
```

Extend `CounterSources`:

```go
type CounterSources struct {
	GRPCAdmission   GRPCAdmissionSource
	IngestLogBudget IngestLogBudgetSource
}
```

Response types, after `grpcAdmissionLevels`:

```go
// ingest_log_budget is COUNTS ONLY, and the absence of a levels half is a
// decision rather than an omission: every ingestLogLimiter is a per-connection
// stack local that dies with its frame, so there is no process-wide "current"
// figure to report without building the shared registry that type explicitly
// refuses to have.
//
// WHAT THE TWO ARMS MEAN, because summing them would be uninterpretable:
// "deduped" is a repeating failure being collapsed to one line per five minutes
// (healthy, and the number is how many chunks that one line represents);
// "suppressed" is a line dropped ENTIRELY because the connection's token bucket
// was empty (an attack or a misconfiguration).
//
// AND WHAT THEY DO NOT COUNT: these are LOG LINES THE BUDGET DROPPED, not
// diagnostics lost. A handler that decides not to log without consulting the
// budget contributes nothing - handleTaskStatus's pgx.ErrNoRows GetTask
// short-circuits before the budget, and handleTaskLog's fence-rejection arm
// never consults it at all (that one is its own item and its own section).
type ingestLogBudgetSection struct {
	Counts ingestLogBudgetCounts `json:"counts"`
}

type ingestLogBudgetCounts struct {
	Deduped    ingestLogKindCounts `json:"deduped"`
	Suppressed ingestLogKindCounts `json:"suppressed"`
}

// ingestLogKindCounts is a STRUCT rather than a map[string]uint64, and that is
// the cardinality rule made structural: the kind set is closed at compile time,
// so named fields make an unbounded key set impossible AND keep both payload
// walks at full reach. A map would need a counterPayloadExemption whose
// predicates descend into it themselves, because an exemption is shape-checked
// but NON-DESCENDING - slice 1 demonstrated a map[string]string with a
// newline-injected key passing both guards.
//
// THESE KEYS ARE A RESPONSE CONTRACT tied to worker's logKind names; see that
// type's comment before renaming anything here.
type ingestLogKindCounts struct {
	TaskLogPersist  uint64 `json:"task_log_persist"`
	BadTaskIDLog    uint64 `json:"bad_task_id_log"`
	BadTaskIDStatus uint64 `json:"bad_task_id_status"`
	StatusGetTask   uint64 `json:"status_get_task"`
	Inventory       uint64 `json:"inventory"`
}
```

`serverCountersResponse` gains one field:

```go
type serverCountersResponse struct {
	StartedAt       time.Time               `json:"started_at"`
	GRPCAdmission   *grpcAdmissionSection   `json:"grpc_admission,omitempty"`
	IngestLogBudget *ingestLogBudgetSection `json:"ingest_log_budget,omitempty"`
}
```

and the handler gains one block, after the `GRPCAdmission` block:

```go
	if src := s.Counters.IngestLogBudget; src != nil {
		d := src.IngestLogDropCounts()
		resp.IngestLogBudget = &ingestLogBudgetSection{Counts: ingestLogBudgetCounts{
			Deduped:    ingestLogKindCountsFrom(d.Deduped),
			Suppressed: ingestLogKindCountsFrom(d.Suppressed),
		}}
	}
```

with:

```go
func ingestLogKindCountsFrom(k worker.IngestLogDropsByKind) ingestLogKindCounts {
	return ingestLogKindCounts{
		TaskLogPersist:  k.TaskLogPersist,
		BadTaskIDLog:    k.BadTaskIDLog,
		BadTaskIDStatus: k.BadTaskIDStatus,
		StatusGetTask:   k.StatusGetTask,
		Inventory:       k.Inventory,
	}
}
```

Add `"relay/internal/worker"` to the file's imports, and extend the "HOW A FUTURE SECTION ATTACHES ITSELF" block's `internal/worker` bullet with: `// (ingest_log_budget is the live example: *worker.Handler satisfies IngestLogBudgetSource and returns worker.IngestLogDrops directly.)`

- [ ] **Step 4: Extend the payload contract and the bytes walk (REQUIRED edits to shipped tests)**

`counterPayloadLeaves` gains ten entries, appended after the existing six:

```go
	"ingest_log_budget.counts.deduped.task_log_persist",
	"ingest_log_budget.counts.deduped.bad_task_id_log",
	"ingest_log_budget.counts.deduped.bad_task_id_status",
	"ingest_log_budget.counts.deduped.status_get_task",
	"ingest_log_budget.counts.deduped.inventory",
	"ingest_log_budget.counts.suppressed.task_log_persist",
	"ingest_log_budget.counts.suppressed.bad_task_id_log",
	"ingest_log_budget.counts.suppressed.bad_task_id_status",
	"ingest_log_budget.counts.suppressed.status_get_task",
	"ingest_log_budget.counts.suppressed.inventory",
```

`TestCounterPayloadBytesCarryNoIdentifiers` wires **every** section, as its own comment requires, so its `Server` literal gains one field:

```go
		Counters: CounterSources{
			GRPCAdmission: fakeAdmissionSource{s: netlimit.Stats{
				Counts: netlimit.RefusalCounts{RefusedTotal: 11, RefusedPerIP: 22},
				Levels: netlimit.Occupancy{LiveTotal: 33, DistinctSources: 44, MaxPerSource: 55},
			}},
			IngestLogBudget: fakeIngestLogSource{d: tenDistinctDrops()},
		},
```

**No assertion in either shipped guard may change.** `counterPayloadAllowList` gains **no entry**: every new leaf is a `uint64`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run 'TestServerCounters|TestCounterPayload' -v`
Expected: PASS, including both payload walks with sixteen leaves each.

- [ ] **Step 6: Commit**

```bash
git add internal/api/
git commit -m "feat(api): serve the ingest_log_budget section of /v1/server/counters"
```

---

### Task 8: Wire the handler main serves gRPC on

**Files:**
- Modify: `cmd/relay-server/http_server.go:23-49`, `:85-103`
- Modify: `cmd/relay-server/main.go:215-230`
- Modify: `cmd/relay-server/counters_wiring_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `cmd/relay-server/counters_wiring_test.go`:

```go
// TestBuildHTTPServer_ServesTheWiredHandlersIngestSection is EXECUTED: it builds
// the server the way main does and reads the section back through the real
// admin-gated route.
//
// SAY WHAT IT DOES NOT BUY. It proves the section is served from a Handler that
// buildHTTPServer was given; it does NOT prove the numbers move, because moving
// them requires the gRPC recv goroutine and a registered agent. That proof is
// TestConnect_IngestDropCountsSurviveAndAggregateAcrossConnections in
// internal/worker's integration lane, and the join between the two - "the
// Handler serving gRPC is the Handler reporting counts" - is the identifier
// property checked in TestServerCountersIsWiredByMain below.
func TestBuildHTTPServer_ServesTheWiredHandlersIngestSection(t *testing.T) {
	h := worker.NewHandler(nil, nil, worker.NewRegistry(), events.NewBroker(), func() {})
	srv := buildHTTPServer(httpServerDeps{
		addr:         "127.0.0.1:0",
		q:            store.New(stubAdminDB{}),
		agentHandler: h,
	})

	top := countersAsAdmin(t, srv)
	require.Contains(t, top, "ingest_log_budget",
		"a wired Handler must produce the section from the moment the server is built. An absent "+
			"section reads as 'this build has no ingest log budget', which is false and is exactly "+
			"the distinction this payload exists to preserve.")

	var section struct {
		Counts struct {
			Deduped    map[string]uint64 `json:"deduped"`
			Suppressed map[string]uint64 `json:"suppressed"`
		} `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(top["ingest_log_budget"], &section))
	require.Len(t, section.Counts.Deduped, 5, "one key per kind")
	require.Len(t, section.Counts.Suppressed, 5, "one key per kind")
}

// TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent. `var h
// *worker.Handler` conditionally assigned stores a TYPED nil in the interface,
// which is not == nil, so the handler's `src != nil` is true and the method call
// dereferences a nil receiver - a goroutine stack trace per admin request,
// inside the feature whose subject is bounding log volume.
//
// The fix belongs at the wiring boundary, where the concrete type still makes
// the distinction visible, and NOT in a nil-tolerant snapshot method: that would
// turn an unwired control into a section of zeros.
func TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent(t *testing.T) {
	var unwired *worker.Handler
	srv := buildHTTPServer(httpServerDeps{
		addr:         "127.0.0.1:0",
		q:            store.New(stubAdminDB{}),
		agentHandler: unwired,
	})

	top := countersAsAdmin(t, srv)
	require.NotContains(t, top, "ingest_log_budget",
		"a nil handler must leave the section ABSENT, never present-and-zero, and must never panic")
	require.Contains(t, top, "started_at")
}
```

Add `"relay/internal/events"` and `"relay/internal/worker"` to the test file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/relay-server/ -run TestBuildHTTPServer -v`
Expected: FAIL to build - `unknown field agentHandler`. Vacuous red; M15 and M18 in the battery are the discriminating runs.

- [ ] **Step 3: Wire it**

`http_server.go`, a new field after `grpcAdmission`:

```go
	// agentHandler is the worker.Handler that serves gRPC, and it is typed
	// CONCRETELY rather than as api.IngestLogBudgetSource for the same reason
	// grpcAdmission is: a (*worker.Handler)(nil) stored in that interface is NOT
	// nil, so the counters handler's `src != nil` would be true and the snapshot
	// call would dereference a nil receiver.
	//
	// IT MUST BE THE SAME HANDLER main REGISTERS WITH
	// RegisterAgentServiceServer. A second Handler would count its own
	// (permanently zero) drops while the real ones went unread - an endpoint
	// reporting that a log budget has suppressed nothing is worse than no
	// endpoint. TestServerCountersIsWiredByMain checks the identifier; nothing
	// executable can check it from here.
	agentHandler *worker.Handler
```

`buildHTTPServer`, replacing the single `s.Counters = ...` assignment:

```go
	// A nil source leaves its section ABSENT, which is the payload's own
	// vocabulary for "this control is not wired on this replica". It is
	// deliberately NOT collapsed into a section of zeros, and no snapshot method
	// is made nil-tolerant: zeros mean "the control ran and stopped nothing", and
	// merging the two is the exact defect the endpoint exists to fix.
	//
	// PER SECTION, not per struct: each control is wired or not on its own.
	if d.grpcAdmission != nil {
		s.Counters.GRPCAdmission = d.grpcAdmission
	}
	if d.agentHandler != nil {
		s.Counters.IngestLogBudget = d.agentHandler
	}
```

`main.go:215-230`, one field in the deps literal, after `grpcAdmission`:

```go
		grpcAdmission:     grpcLis,
		agentHandler:      agentHandler,
```

- [ ] **Step 4: Generalize the wiring guard**

In `counters_wiring_test.go`, `TestServerCountersIsWiredByMain`: replace the single-dependency block (`:280-340`) with a table, keeping every existing assertion message intact for the `grpcAdmission` row, and add the same-object check. Extend the test's own doc comment with a paragraph explaining the second row.

```go
	// EVERY WIRED SOURCE, not just the first one. The walk below is run once per
	// row: a section whose source is fed a conditionally-assigned local reaches
	// the endpoint as a typed nil and vanishes on every deployment that takes the
	// branch, which reads exactly like a control that has never stopped anything.
	deps := []struct {
		field     string
		mustReach string
		what      string
	}{
		{"grpcAdmission", "Wrap", "the netlimit listener bound in main's body"},
		{"agentHandler", "NewHandlerWithGrace", "the worker.Handler bound in main's body"},
	}

	depArg := map[string]*ast.Ident{}
	for _, st := range body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok {
			continue
		}
		for _, r := range as.Rhs {
			ce, ok := r.(*ast.CallExpr)
			if !ok {
				continue
			}
			if fn, ok := ce.Fun.(*ast.Ident); !ok || fn.Name != "buildHTTPServer" {
				continue
			}
			require.Len(t, ce.Args, 1)
			cl, ok := ce.Args[0].(*ast.CompositeLit)
			require.True(t, ok, "buildHTTPServer must be called with an httpServerDeps composite literal "+
				"at the call site, so that every dependency is readable there")
			for _, e := range cl.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				k, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				id, isIdent := kv.Value.(*ast.Ident)
				require.True(t, isIdent || !isWiredDep(deps, k.Name),
					"httpServerDeps.%s must be fed a plain identifier bound in main's body, not %T",
					k.Name, kv.Value)
				if isIdent {
					depArg[k.Name] = id
				}
			}
		}
	}

	chainNames := map[string]bool{srvName: true}
	for _, d := range deps {
		argIdent := depArg[d.field]
		require.NotNil(t, argIdent,
			"buildHTTPServer is called with no %s field, so that section is absent and the endpoint "+
				"reports nothing about a control that IS running - which reads exactly like a control "+
				"that has never stopped anything", d.field)

		seen := map[string]bool{}
		queue := []string{argIdent.Name}
		reached := false
		for len(queue) > 0 && !reached {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			if name == d.mustReach {
				reached = true
				break
			}
			queue = append(queue, from[name]...)
		}
		require.True(t, reached,
			"httpServerDeps.%s is fed %q, which does not derive from %s through an UNCONDITIONAL "+
				"assignment in main's body. A local assigned inside an if - the natural shape for "+
				"'only build it when configured' - reaches the endpoint as a typed nil and the section "+
				"vanishes on every deployment that does not take the branch.",
			d.field, argIdent.Name, d.mustReach)
		for n := range seen {
			chainNames[n] = true
		}
	}

	// THE COUNTERS MUST COME FROM THE HANDLER THAT SERVES gRPC. Feeding
	// buildHTTPServer a second worker.Handler compiles, passes every check above
	// and leaves the endpoint reporting a permanently empty log budget while the
	// real one fills up - which is worse than no endpoint, because it is a
	// confident zero.
	var registered []string
	ast.Inspect(file, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "RegisterAgentServiceServer" || len(ce.Args) != 2 {
			return true
		}
		if id, ok := ce.Args[1].(*ast.Ident); ok {
			registered = append(registered, id.Name)
		}
		return true
	})
	require.Len(t, registered, 1,
		"main must register exactly one AgentService implementation; found %v", registered)
	require.Equal(t, registered[0], depArg["agentHandler"].Name,
		"main serves gRPC on %q but reports ingest log counters from %q. They must be the same "+
			"Handler.", registered[0], depArg["agentHandler"].Name)
```

and the final assignment-count loop iterates `chainNames` instead of `chain`:

```go
	for name := range chainNames {
		if len(from[name]) == 0 && assignedAnywhere[name] == 0 {
			continue
		}
		require.Equal(t, 1, assignedAnywhere[name], /* ...existing message verbatim... */ name, assignedAnywhere[name])
	}
```

with the small helper:

```go
func isWiredDep(deps []struct {
	field     string
	mustReach string
	what      string
}, name string) bool {
	for _, d := range deps {
		if d.field == name {
			return true
		}
	}
	return false
}
```

(If the anonymous struct type is awkward to pass, declare a named `type wiredDep struct{ field, mustReach, what string }` at file scope and use it in both places. Do not drop the check.)

- [ ] **Step 5: Run the guards, then attack them**

Run: `go test ./cmd/relay-server/ -v`
Expected: PASS, including the two pre-existing wiring tests unchanged.

Then run these evasions in the isolated mutation worktree, one at a time, restoring between each, and record the observed output of every one:

1. Delete `agentHandler: agentHandler,` from the deps literal -> `TestServerCountersIsWiredByMain` RED **and** the executed test... is not RED (it builds its own deps). Confirm the AST guard is what catches it.
2. `agentHandler := worker.NewHandlerWithGrace(...)` then `if os.Getenv("X") != "" { agentHandler = nil }` -> RED on the assignment count (this is the `grpcLis = nil` shape one variable to the left).
3. A second handler: `otherHandler := worker.NewHandlerWithGrace(...)` fed to `agentHandler:` while `RegisterAgentServiceServer` keeps the first -> RED on the same-object check.
4. `agentHandler: wrapCounters(agentHandler)` (a helper call in a sibling file) -> RED on "must be fed a plain identifier".
5. Drop the `if d.agentHandler != nil` filter in `buildHTTPServer` -> `TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent` RED with a nil-pointer panic.
6. Delete `s.Counters.IngestLogBudget = d.agentHandler` -> `TestBuildHTTPServer_ServesTheWiredHandlersIngestSection` RED.
7. Import alias: `import w2 "relay/internal/worker"` and `w2.NewHandlerWithGrace` -> confirm the walk still resolves `NewHandlerWithGrace` (it matches the `Sel` ident, not the package). Record the result; if it does NOT hold, that is a finding to report.

- [ ] **Step 6: Commit**

```bash
git add cmd/relay-server/
git commit -m "feat(server): wire the agent handler's ingest log counters into /v1/server/counters"
```

---

### Task 9: README, then the full gates

**Files:**
- Modify: `README.md:1260-1277`

- [ ] **Step 1: Extend the payload example**

Replace the JSON block at `README.md:1260-1268`:

```json
{
  "started_at": "2026-08-21T09:00:00Z",
  "grpc_admission": {
    "counts": { "refused_total": 12, "refused_per_source": 3 },
    "levels": { "live_total": 812, "distinct_sources": 16, "max_per_source": 64 }
  },
  "ingest_log_budget": {
    "counts": {
      "deduped":    { "task_log_persist": 4127, "bad_task_id_log": 0, "bad_task_id_status": 0,
                      "status_get_task": 0, "inventory": 12 },
      "suppressed": { "task_log_persist": 39984, "bad_task_id_log": 0, "bad_task_id_status": 0,
                      "status_get_task": 0, "inventory": 0 }
    }
  }
}
```

- [ ] **Step 2: Add the reading guide**

Insert after the existing `grpc_admission` bullets (`README.md:1275`):

```markdown
- **Reading `ingest_log_budget`.** Agents can drive log volume, so every caller-driven log line on the gRPC receive path is rate-limited per connection; these are the lines that limiter dropped, since `started_at`. **`deduped`** is the healthy arm: the same failure repeated inside a five-minute window, folded into one line - a large number here next to a quiet log is one task failing over and over, and the number is how many occurrences that single line represents. **`suppressed`** is the loud arm: a line dropped entirely because that connection's token bucket was empty. A non-zero `suppressed` means some connection is producing *distinct* failures faster than six lines per minute, which is either an attack or a misconfiguration; `39984` under `task_log_persist` is the difference between "a flood is in progress" and "the fleet looks quiet". The five keys name the log site, not the worker: **there is no per-worker split and there will not be one**, because keying these counters on anything the recv goroutine would have to look up needs a shared map write on the one path that must stay lock-free.
- **What `ingest_log_budget` does NOT count.** These are *log lines the budget dropped*, not diagnostics lost. A handler that decides not to log without consulting the budget - a status update for a task that no longer exists, a log chunk the fence rejected - contributes nothing to these numbers. They also have no `levels` half: each budget is per connection and dies with it, so there is no process-wide current figure to report.
```

- [ ] **Step 3: Run every gate**

```bash
go build ./...
go vet ./...
make vet-integration
make test
```
Expected: all green. Record the top-level unit test count (slice 1 ended at 574) and compare.

```
docker run --rm -v D:/dev/relay/.claude/worktrees/pr-merge-session-961184:/src -w /src golang:1.26 \
  go test -race ./... -timeout 300s
```
Expected: green module-wide. `-race` cannot run on the Windows authoring host (ThreadSanitizer shadow-allocation failure, which affects untouched packages too), so the container is the only valid evidence.

```bash
go test -tags integration -p 1 ./internal/worker/ -timeout 900s
go test -tags integration -p 1 ./internal/api/ -timeout 900s
```
Expected: green (Docker Desktop must be running). Report the observed counts, not "all green".

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: document the ingest_log_budget counters section"
```

---

## Mutation battery

Run in an **isolated detached worktree**, never the shared tree. **Establish a green baseline first**: uniform results mean the harness is broken, not that coverage is good. Every row must be COMPILABLE (a compile error is not a behavioural kill) and killed by the NAMED test.

| # | Mutation | Must go RED |
| --- | --- | --- |
| M1 | Dedupe arm does not `record` | `TestIngestLogLimiter_TheDedupeArmCountsDeduped`, `TestHandleTaskLog_ABadTaskIDFloodCountsTheDedupedArm` |
| M2 | Spend arm does not `record` | `TestIngestLogLimiter_TheEmptyBucketCountsSuppressed`, `TestHandleTaskStatus_ADroppedLineUnderAnEmptyBudgetCountsSuppressed` |
| M3 | Both arms record `ingestDropDeduped` | `TestIngestLogLimiter_TheEmptyBucketCountsSuppressed` |
| M4 | `record` also called on the allowed path (before `l.tokens--`) | `TestIngestLogLimiter_AnAllowedLineCountsNothing` |
| M5 | `snapshot` swaps the two arms | `TestIngestLogCounters_EveryKindIsPublishedDistinctly` (per-half assertion) |
| M6 | `byKind` reads `c.n[kindBadTaskIDStatus]` for `BadTaskIDLog` | `TestIngestLogCounters_EveryKindIsPublishedDistinctly` |
| M7 | `record` indexes `c.n[i-1][arm]` | `TestIngestLogCounters_EveryKindIsPublishedDistinctly` |
| M8 | `record`'s bounds check becomes `if i < 0` (kind 0 counted into slot 0) | `TestIngestLogCounters_AnOutOfRangeKindIsDroppedNotPanicked` |
| M9 | A sixth kind added before `kindCount`, recorded at a new `logKey` site, published nowhere | `TestIngestLogCounters_EveryKindIsPublishedDistinctly` (six values, five fields) |
| M10 | A sixth kind added AFTER `kindCount`, used at a `logKey` site | `TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished` (declared count vs `kindCount`) |
| M11 | A sixth kind declared in a **separate const block** / a **sibling file** | `TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished` (parses the package) |
| M12 | `Connect` allocates the limiter with `&ingestLogCounters{}` | `TestConnect_IngestDropCountsSurviveAndAggregateAcrossConnections` (integration) |
| M13 | Counters made a package-level var shared by all Handlers | `TestIngestLogCounters_TwoHandlersDoNotShareCounts` |
| M14 | `atomic.Uint64` -> plain `uint64` with `++` | `TestIngestLogCounters_ConcurrentDropsFromManyLimitersAreExact` under `-race` (Linux container). **Record the `-cpu=1` result separately** - the exactness half may not kill there; the `-race` half should. |
| M15 | `handleServerCounters` maps `d.Deduped` into `Suppressed` | `TestServerCounters_ReportsTheIngestLogSnapshot` |
| M16 | `ingestLogKindCountsFrom` maps `k.StatusGetTask` into `Inventory` | `TestServerCounters_ReportsTheIngestLogSnapshot` |
| M17 | Section emitted only when non-zero (`if d != (worker.IngestLogDrops{}) { ... }`) | `TestServerCounters_WiredButZeroIngestSectionIsStillPresent` |
| M18 | JSON tag `task_log_persist` renamed to `taskLogPersist` | both payload walks via `counterPayloadLeaves` |
| M19 | `ingestLogKindCounts` replaced with `map[string]uint64` | `TestCounterPayloadCarriesNoIdentifiers` (a map is not an unsigned integer) |
| M20 | `s.Counters.IngestLogBudget = d.agentHandler` deleted | `TestBuildHTTPServer_ServesTheWiredHandlersIngestSection` |
| M21 | The `if d.agentHandler != nil` filter deleted | `TestBuildHTTPServer_TypedNilAgentHandlerLeavesTheSectionAbsent` (nil-receiver panic) |
| M22 | `agentHandler = nil` inside an `if` in main | `TestServerCountersIsWiredByMain` (assignment count) |
| M23 | A second `worker.NewHandlerWithGrace` fed to the deps literal | `TestServerCountersIsWiredByMain` (same-object check) |
| M24 | `agentHandler:` fed a helper call instead of an identifier | `TestServerCountersIsWiredByMain` (plain-identifier check) |

**Known residual, disclosed rather than guarded:** an **untyped** `kindFoo = 9` declared inside the `logKind` const block and used at a `logKey` site fails M11's walk for the "not a counted constant" reason rather than the "outside the sentinel" reason. The failure message states both, so the diagnosis is not misleading. No third guard is built for it.

---

## Test-lane notes

- **go-ci runs `go test -race ./...` on `ubuntu-latest` with NO build tag and NO `-cpu` flag** (`.github/workflows/go-ci.yml:26-34`), plus `make vet-integration`. Everything that can run without Docker must therefore be in the default lane - which is why Task 4's acceptance proof is not tagged, and why Task 6's is (it needs Postgres, checked rather than assumed).
- **One test's kill depends on parallelism: `TestIngestLogCounters_ConcurrentDropsFromManyLimitersAreExact`** (M14). Its exactness assertion needs more than one CPU to observe a lost update; its `-race` half does not, because ThreadSanitizer is happens-before based. CI has 2-4 vCPUs, so both halves are live there. The test's comment states the asymmetry, per the slice-1 lesson that a test can be robust and inert on the same machine.
- **`-race` cannot run on the Windows authoring host.** Use `golang:1.26` in Docker, as in Task 5 and Task 9.
- **No test in `internal/worker` calls `t.Parallel`**, which is what makes `captureUnitLog`'s process-global redirect safe. If that changes, it is a finding.

---

## Self-review

**Spec/item coverage.** Every Done-When bullet of `idea-2026-08-15-ingest-log-suppression-is-uncounted` maps to a task: counter split by arm proven by a handler-layer flood test (Tasks 3-4, 6); readable through the endpoint (Tasks 7-8); no-mutex hot path preserved with `-race` behind it (Tasks 2, 5); no new log line, round trip, goroutine, queue or lock (standing constraints, Task 3's diff is two `record` calls); unreadable by an agent (structural, C8); the same read surface as the sibling item (Task 7 extends it); the `l == nil` arm not counted with the reason at the site (Task 3, Step 4); the `logKind` comment amended in the same PR with the name mapping pinned (Tasks 1 and 7); per-kind objects as Go structs with no exemption (Task 7); an unwired section ABSENT (Tasks 7-8). Spec section 10.2's contents list is fully covered.

**Placeholder scan.** No TBD, no "add error handling", no "similar to Task N". Every code step carries the code.

**Type consistency.** `logKind`/`kindCount`/`ingestDropDeduped`/`ingestDropSuppressed`/`ingestDropArms`/`ingestLogCounters.record`/`.snapshot`/`.byKind`/`IngestLogDrops`/`IngestLogDropsByKind`/`Handler.ingestDrops`/`Handler.IngestLogDropCounts`/`api.IngestLogBudgetSource`/`CounterSources.IngestLogBudget`/`ingestLogBudgetSection`/`ingestLogBudgetCounts`/`ingestLogKindCounts`/`ingestLogKindCountsFrom`/`httpServerDeps.agentHandler` are each spelled identically at every appearance above.

---

## Backlog effects - proposed, not filed (the conductor files these)

- **CLOSE `idea-2026-08-15-ingest-log-suppression-is-uncounted`** via `/backlog close` (which does the `git mv` into `docs/backlog/closed/`). **This slice can close the item outright**: every Done-When bullet is in scope and none is deferred. The resolution note should record the three refutations the item itself will be wrong about if anyone re-reads it later - the `[5]` array, the package-level home, and "values may still be renumbered freely".
- **No amendment is needed to the two remaining sibling items** by this slice: it changes nothing about their scope. It does leave `internal/worker` with a counters home that `idea-2026-08-14-tasklog-fence-rejection-is-unobservable` (slice 3) reuses, and a `CounterSources` precedent that says **one section per source field** - worth a one-line note on that item if the conductor is amending it anyway.
- **No new items are proposed.** If Phase 4 surfaces something, it files its own.

---

## Residual risk

1. **The "same object" property is checked syntactically, not executed.** Nothing in `cmd/relay-server` can move an ingest counter (it needs the recv goroutine and a registered agent), so `TestServerCountersIsWiredByMain`'s identifier check is the join between "this Handler serves gRPC" and "this Handler reports counts". That is one rung below the ladder's top. Mitigated by: the identifier check plus the assignment-count property plus the executed presence/absence pair, and by the fact that main never holds the `*api.Server` at all.
2. **`Handler` becomes non-copyable.** `go vet`'s copylocks check will flag any existing `*h` copy. None is expected, and Task 3 Step 1 runs vet explicitly; if one exists, the engineer must report it rather than switch the field to a pointer silently.
3. **A sixth kind declared as an untyped constant** escapes the AST count for the reason above. Disclosed in the guard's own comment.
4. **Per-replica, not fleet-wide**, and zeroed by a restart. Inherited from the shipped contract; `started_at` is what makes it readable. Documented, not fixed.
5. **`TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished` parses `.` relative to the package directory.** That is how `go test` runs, and `cmd/relay-server/trailing_log_window_test.go` and `counters_wiring_test.go` already rely on the same working directory. If a future build system changes that, the test fails loudly rather than silently.
6. **`TestServerCountersIsWiredByMain` is being edited**, and it is the guard that took seven evasions to get right. The plan keeps every existing assertion message and adds rows rather than rewriting the walk, and Task 8 Step 5 re-runs the evasions that beat its predecessors. Any assertion that must *change* rather than generalize is a finding to report.
