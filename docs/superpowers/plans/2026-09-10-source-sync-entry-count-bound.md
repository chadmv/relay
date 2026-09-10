# A count bound on `source.sync` - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `maxSyncEntries = 512` to `internal/jobspec`, checked immediately after the existing `len(s.Sync) == 0` refusal and before every per-entry rule, so one source spec's total sync entries - includes and exclusions together - are bounded at both ends.

**Architecture:** One new package-level constant and one two-line comparison inside `validateSourceSpec`. No new function, no new type, no new package, no wiring. Every ingest path and every stored-spec path already reaches `validateSourceSpec` through the single `jobspec.Validate` entry point, so the bound is retroactive over stored `scheduled_jobs.job_spec` rows for free and requires no caller change anywhere.

**Tech Stack:** Go 1.26, standard library only in `internal/jobspec` (it imports `errors`, `fmt`, `regexp`, `sort`, `strings` and nothing else), `testify/require` in tests. No database, no Docker, no build tags.

**Spec:** `docs/superpowers/specs/2026-09-10-source-sync-entry-count-bound.md`
**Closes:** `idea-2026-09-04-source-sync-has-no-entry-count-bound`

---

## Slice independence declaration

**This is ONE slice, ONE PR, ONE session. It is NOT a multi-stage plan. Do not run `/backlog phases` on it.**

There is no frontend slice and no second backend chain. `web/` is untouched: `web/src/jobs/specTemplate.ts`'s `validateSpecText` names `source` as a rule it deliberately does not implement, so there is nothing under `web/` to keep in step. `python/` is untouched: `python/src/relay/models.py` already assigns the sibling-dependent source rules to the server in `Sync`'s own docstring (verified - the docstring names "how many exclusions one spec may carry" among them, and `Source._at_least_one_sync` duplicates only the LOWER end of this range with independent wording), and copying the number into a separately released package is refused for the reason `maxRetries`'s comment gives.

Phase 3 is therefore a **single backend lane**, executed serially. Tasks 0 through 7 below are strictly sequential; do not parallelise them.

---

## Scope fence - THREE SIBLING LANES ARE RUNNING CONCURRENTLY

Read this before touching anything. You are LANE S. You own:

- `internal/jobspec/jobspec.go`
- `internal/jobspec/sync_bounds_test.go` (new)
- one helper and two table rows in `internal/api/job_spec_source_test.go`
- exactly one row of `README.md` (the `sync` row of the Source workspaces field table)

**You must not edit, and must not "fix" if you see it red:**

| Path | Owner |
|---|---|
| `internal/worker/`, `internal/agent/source/perforce/`, `internal/store/query/worker_workspaces.sql` | LANE W |
| `internal/schedrunner/` (**including `stored_spec_count_bounds_test.go`**), `cmd/relay-server/main.go`, `internal/store/query/scheduled_jobs.sql` | LANE B |
| `internal/testsupport/pgdsn/` | LANE E |
| `internal/scheduler/dispatch.go` | unowned; the `BaselineHashFromAPISpec` hoist is a backlog proposal, not work in this slice |

**`internal/testsupport/pgdsn/` has a guard that is RED on a clean tree inside the `golang:1.26` container.** If you see it fail, it is not yours. Do not diagnose it, do not fix it, do not report it as a regression caused by this slice. Note it and move on.

**Do NOT add a fourth row to `internal/schedrunner/stored_spec_count_bounds_test.go`.** It is the obvious place a reader would look for stored-spec coverage of this bound, and the design deliberately declines it, on that file's own recorded argument (verified in the tree - the file's header comment states it): its three rows plus its control already prove that `ValidateStoredSchedule` carries a bound's OWN message, this bound reaches that function through the same single `jobspec.Validate` call with no new wiring, and both callers' wiring is pinned message-agnostically elsewhere (`TestValidateStoredSpecsOnStartup` and the PATCH clear-decision tests in `internal/api`, neither of which cares which rule produced the message). A fourth row would add one vocabulary item to a delegation already proven, in a file another lane owns, and would collide. If the conductor later decides the row is wanted, it belongs to LANE B and is sequenced after this PR.

Because concurrent agents share one git index, **every `git commit` in this plan uses an explicit pathspec** (`git commit ... -- <paths>`). Never `git add -A`, never `git add .`, never a bare `git commit -a`.

---

## What the planner refuted or corrected in the spec

Read this once. Four items; two of them change what you write.

1. **Section 5's "the `excluded > maxSyncExclusions` check cannot move earlier because it needs the count that loop produces" - verified, with one nuance.** Confirmed against `internal/jobspec/jobspec.go`: `excluded` is initialised at line 553, incremented at line 577 inside the per-entry loop, and read at line 589. So the check does depend on the loop's output as the code is written. The word "cannot" is one notch stronger than the tree supports - a dedicated pre-pass counting only `e.Exclude` would move it earlier - but section 3 already rejects a second counting pass for an independent reason, so nothing in the design turns on this. **Consequence for you: none. Write the check where section 5 says.**

2. **Section 4 conflates "the smallest legal sync entry" with "the smallest `#head` entry", and section 13 item 1 inherits the conflation.** By hand count against the four rev regexps in `jobspec.go` lines 68-71: `{"path":"//","rev":"#head"},` is 28 bytes, which is what section 4 states and it is arithmetically right for a `#head` entry (1 MiB / 28 = about 37,400, so the "order of 37,000" division is also right). But `revCLRe` (`^@\d+$`) and `revNumRe` (`^#\d+$`) both accept a two-character rev, so `{"path":"//","rev":"@1"},` is **25 bytes** and is legal, cheaper, and not a `#head` entry. The two numbers bound different axes: 28 bytes bounds the p4-round-trip axis, 25 bytes bounds the entry-count axis. **Consequence for you: Task 0 Step 1 measures BOTH, and any sentence you later write must say which axis its number is on.**

3. **Section 10's arithmetic - the conclusion survives, but its "1.5x" and the "8x" it is compared against are computed on different bases.** Checked: 5000 tasks x 1 entry = 5000, and 2000 tasks x 5 entries = 10,000, both correct. The "8x" figure it borrows from `maxCommandsPerJob`'s comment is *legitimate high end* against *what the body expresses* (about 20,000 against 150,000). The "1.5x" figure here is *the proposed job-wide cap* against *what the body expresses* (25,000 against about 37,000). Like-for-like, the sync-axis window is legitimate-high-end 10,000 against a body ceiling near 37,000 to 42,000, i.e. roughly **3.7x to 4.4x**, not 1.5x. That is still less than half the command axis's 8x, and applying this project's own 2.5x-to-5x multiplier band to a 10,000 high end puts a job-wide cap at 25,000 to 50,000 - **at or above what a 1 MiB body can express**, which is exactly the "two caps whose product exceeds what the body already permits reduce nothing" failure `maxCommandsPerJob`'s comment names. **So section 10's conclusion holds, and holds robustly across the whole plausible range of `C`: a job-wide aggregate cap is refused.** The printed "1.5x" is not a number to reproduce anywhere.

4. **Section 5's complement claim ("no existing test can depend on the old error ordering, because every source-spec fixture in the tree is small") is a claim about the complement and is NOT settled.** The spec itself flags it as needing a search. Two things confirmed by targeted read, which is exactly the instrument the claim needs *not* to rest on: `manySyncExclusions(17)` in `internal/api/job_spec_source_test.go` builds 1 include + 17 exclusions = 18 entries, and `internal/jobspec` currently contains **no source-spec test at all** (a `^func ` census of the package's four files shows no helper or test naming `Sync` or `SourceSpec`). **Consequence for you: Task 0 Step 3 is a tree-wide SEARCH with a recorded hit count and a recorded command. Do not inherit the claim as settled.**

One further observation that changes a test: because `internal/jobspec` has no source-spec test today, the control the spec designates (`TestValidateJobSpec_Source_Perforce`'s happy-path row) lives in a **different package**. A same-package control is cheaper and stronger, so this plan adds one *in addition to* keeping that row green and untouched. It is one `require.NoError` on an ordinary three-entry spec.

---

## File structure

| File | Change | Responsibility |
|---|---|---|
| `internal/jobspec/jobspec.go` | Modify: insert a constant before line 299 (`maxSyncExclusions`), insert a 3-line check after line 552 (the `len(s.Sync) == 0` refusal), and in Task 6 append one paragraph to the new constant's comment | The bound itself and its argument |
| `internal/jobspec/sync_bounds_test.go` | Create | Every property of the bound: both ends, the boundary, placement precedence against three later rules, the axis (exclusions count, pinned revs count), and a same-package control |
| `internal/api/job_spec_source_test.go` | Modify: one helper + two table rows | That the rule reaches the API layer's exported type aliases (`api.SyncEntry = jobspec.SyncEntry`, declared at `internal/api/job_spec.go:17-18`), which is a different property from the rule existing |
| `README.md` | Modify: one row | The operator-facing statement of the bound and its retroactivity |
| `internal/api/zz_measure_body_ceiling_test.go` | Create in Task 0, **DELETE in Task 0** | Throwaway measurement harness. Never committed. |

---

## Which retroactive paths get a guard here, and why the others do not

`jobspec.go`'s `maxRetries` comment (lines 92-134) enumerates the five paths `Validate` reaches with stored data. Decide once, here, so nobody adds a row "to be safe":

| Path | Guard added by this slice? | Why |
|---|---|---|
| `schedrunner.fireOne` (direct `Validate`, records into `last_error`) | **No** | Reaches the new rule through the same single `Validate` call as every other rule. Its wiring is already pinned message-agnostically by `internal/schedrunner`'s runner tests, which use the retries message and do not care which rule produced it. A row here adds one vocabulary item to a proven delegation - and the file is LANE B's. |
| `api.handleRunScheduledJobNow` (direct `Validate`, decides 400 vs 500) | **No** | Same. The status-code decision is a property of the call site, already pinned, and is independent of which rule fired. |
| `schedrunner.ValidateStoredSpecsOnStartup` via `ValidateStoredSchedule` | **No** | See the scope fence. `internal/schedrunner/stored_spec_count_bounds_test.go` is LANE B's file and its own comment argues the marginal row is not worth it. |
| `api.handlePatchScheduledJob`'s clear-decision via `ValidateStoredSchedule` | **No** | Same function, same argument. |
| `jobcreate.CreateJobFromSpec` | **No** | It calls `Validate` and returns its error unchanged; nothing about this rule is distinguishable there. |
| `internal/api`'s exported spec type aliases | **YES - two rows** | This one is genuinely different. `internal/api/job_spec.go` declares `SourceSpec = jobspec.SourceSpec` and `SyncEntry = jobspec.SyncEntry` as **type aliases**, and `ValidateJobSpec` takes a value rather than a pointer. The rows prove the rule survives that surface, at the boundary in both directions. They sit beside the sibling bound's `"sixteen exclusions is allowed"` / `"seventeen exclusions"` pair, so the two source count bounds read as a pair. |

The rule the table encodes: **a caller gets a row only when it can independently break.** Five of the six cannot - they share one function call - and adding a row to each would be five copies of one fact plus two cross-lane collisions.

---

## Task 0: Measurements and preconditions

**No production code is written in this task and nothing is committed.** Four measurements. Each one has a command and an explicit "record the number" output. **A number that was not produced by running the step does not exist, and no later task may use it.** Write every result into a file you keep for the PR body - use your session scratchpad, e.g. `%TEMP%\relay-lane-s\task0.md`, not a file inside the repo.

- [ ] **Step 1: Establish the body ceiling `C` by CONSTRUCTING a maximal body**

The spec's "order of 37,000" is hand arithmetic, explicitly marked unverified, and the planner found it is the ceiling for the `#head` axis rather than for the entry-count axis (see refutation 2 above). Measure both.

Create `internal/api/zz_measure_body_ceiling_test.go` with **exactly** this content:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TEMPORARY MEASUREMENT HARNESS. Delete before any commit; never git add it.
func TestMeasureSyncEntryBodyCeiling(t *testing.T) {
	const prefix = `{"name":"m","tasks":[{"name":"t","command":["true"],` +
		`"source":{"type":"perforce","stream":"//","sync":[`
	const suffix = `]}}]}`

	for _, tc := range []struct {
		name  string
		entry string
	}{
		{"cheapest entry the decoder accepts", `{"path":"//","rev":"@1"},`},
		{"cheapest entry that costs a p4 round trip", `{"path":"//","rev":"#head"},`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// One trailing comma is trimmed, so n entries cost n*len(entry)-1.
			n := (maxBodyBytes - len(prefix) - len(suffix) + 1) / len(tc.entry)
			var b strings.Builder
			b.WriteString(prefix)
			for i := 0; i < n; i++ {
				b.WriteString(tc.entry)
			}
			body := strings.TrimSuffix(b.String(), ",") + suffix

			var spec JobSpec
			req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(body))
			rec := httptest.NewRecorder()
			if !readJSON(rec, req, &spec) {
				t.Fatalf("decoder REFUSED the body: status=%d body=%s", rec.Code, rec.Body.String())
			}
			t.Logf("MEASURED: entryBytes=%d bodyBytes=%d maxBodyBytes=%d acceptedEntries=%d",
				len(tc.entry), len(body), maxBodyBytes, len(spec.Tasks[0].Source.Sync))
		})
	}
}
```

(`maxBodyBytes` is `1 << 20`, declared at `internal/api/server.go:379`; `readJSON` has signature `(w http.ResponseWriter, r *http.Request, v any) bool` at `internal/api/server.go:384`. Both are unexported, which is why the harness lives in package `api`.)

Run:

```bash
go test ./internal/api/ -run TestMeasureSyncEntryBodyCeiling -v -count=1
```

Expected: PASS, with two `MEASURED:` lines.

**Record here, verbatim from the output:**

- `C_min` = accepted entries for the `@1` entry: __________
- `C_head` = accepted entries for the `#head` entry: __________
- The `entryBytes` the harness printed for each. **Confirm they are 25 and 28. If they are not, the hand count in the spec is wrong and you must say so in the PR body.**

**If the harness fails to decode**, that is itself the measurement: record the status and the error, and record that `C` could not be established. Do not substitute arithmetic.

**Now DELETE the file:**

```bash
rm internal/api/zz_measure_body_ceiling_test.go
git status --porcelain internal/api/
```

Expected: the second command prints nothing for `zz_measure_body_ceiling_test.go`.

**Carry this constraint into every later task: no factor derived from `C` may reach a code comment, a commit message, a PR body sentence or README until these numbers exist.** If Step 1 was not run, Task 6 is SKIPPED, not guessed.

- [ ] **Step 2: Measure argv byte length at 512 entries**

`internal/agent/source/perforce/client.go:202` builds `args := append([]string{"-c", client, "sync", "--parallel=4"}, specs...)`, and `internal/agent/source/perforce/perforce.go:429` builds each element as `cp + rev`, where `cp` is `toClientPath`'s output (client form, `//<clientName>/<remainder>`) and `rev` is either the literal rev or `@<resolved CL>`. Excluded entries contribute no argv element (`perforce.go:409-411`).

Compute by hand from those two lines, using a realistic shape, and show your working:

- client name shape: read `clientName`'s construction in `internal/agent/source/perforce/` and record its actual length for a typical host. The existing test literal `relay_h_ab12cd` in `perforce_clientpath_test.go:20` is 14 characters - confirm whether that is representative of what the real constructor emits.
- one argv element: `//<clientName>/<remainder>@<CL>`. Use a realistic remainder, e.g. `Content/Characters/Hero/Meshes/...`, and a realistic 7-digit changelist.
- total: `len("p4") + len("-c") + len(client) + len("sync") + len("--parallel=4") + 512 * len(element)`, plus one separator byte per argument.

**Record here:**

- bytes per argv element: __________
- total command-line bytes at 512 entries: __________
- the documented platform limits you compared against, with their source (Windows `CreateProcess` `lpCommandLine`, and the Linux `MAX_ARG_STRLEN` / `ARG_MAX` pair): __________
- verdict: does the computed total sit **under, near, or over** the tightest documented limit? __________

**Do NOT report whether exec actually fails at that boundary.** Nothing in this slice runs it, and section 10 states the hazard as open on purpose. Report the arithmetic and the limit, and nothing about observed behaviour.

- [ ] **Step 3: SEARCH the whole tree for any fixture building more than 512 sync entries**

A new bound is retroactive over fixtures as well as over stored rows, and a fixture that goes vacuous or red is cheap to find now and expensive to diagnose later. **This is a search, not a targeted read** - the spec's claim is about the complement, so opening one file proves nothing.

Run all five, from the worktree root, and record the hit count of each:

```bash
rg -n --stats "SyncEntry"
rg -n --stats "\"sync\"\s*:\s*\["
rg -nU --stats --multiline "for\s[^\n]*\{[^{}]*SyncEntry"
rg -n --stats "manySyncExclusions|syncIncludes|syncSpecOf|manySyncIncludes"
rg -n --stats "sync" python/ web/ docs/ -g '*.py' -g '*.ts' -g '*.tsx' -g '*.json'
```

**Record here, per command: the hit count, and for every hit that builds entries in a LOOP, the loop bound.**

- command 1 hits: ______ ; loop-built fixtures among them: ______
- command 2 hits: ______
- command 3 hits: ______
- command 4 hits: ______
- command 5 hits: ______
- **Largest sync-entry fixture found anywhere in the tree, with file:line and its entry count:** __________

Planner's preliminary read, **to be CONFIRMED or REFUTED by the counts above and not assumed**: a `SyncEntry` census across the tree returned 232 occurrences in 41 files, the densest Go test files being `internal/agent/source/perforce/sourcekey_test.go` (24), `internal/api/job_spec_source_test.go` (17) and `internal/agent/source/perforce/perforce_test.go` (13), and the only loop-builder identified was `manySyncExclusions` at 17. **If your search finds a fixture over 512, stop and report it before writing any code** - it changes the slice.

- [ ] **Step 4: Establish a green baseline BEFORE any mutation**

The mutation battery in Task 3 is meaningless without one. A uniform result across mutations means a broken harness, and a compile error is not a kill.

```bash
go build ./... && go vet ./... && go test ./... -count=1
```

**Record here:**

- `go build` result: __________
- `go vet` result: __________
- `go test ./... -count=1`: total packages, and the name of every package that is NOT `ok` or `no test files`: __________

**If any package is red at HEAD, record it as pre-existing with its failure text before you change anything.** Per this project's rule, measure a red gate both ways: you will need this list again in Task 7 to say whether a failure is yours.

---

## Task 1: The failing tests

**Files:**
- Create: `internal/jobspec/sync_bounds_test.go`
- Modify: `internal/api/job_spec_source_test.go` (append one helper after `manySyncExclusions`, which currently ends at line 197; insert two table rows after the `"seventeen exclusions"` case at lines 151-153)

Nothing is committed in this task.

- [ ] **Step 1: Write `internal/jobspec/sync_bounds_test.go`**

Create the file with exactly this content:

```go
package jobspec

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// syncStream is the stream every fixture in this file is rooted at. Short on
// purpose: the fixtures run to 513 entries and every path is built from it.
const syncStream = "//s"

// syncIncludes returns n include entries under syncStream, each a DISTINCT,
// NON-NESTING path at #head.
//
// The distinctness is the point. No entry sits under another, so neither the
// coverage rule nor the swallow rule can fire and a refusal in an over-count
// case can only have come from the count. #head is the rev because it is the
// axis the bound is argued from: one p4 round trip per entry inside the task's
// own prepare phase, repeated on every attempt.
func syncIncludes(n int) []SyncEntry {
	out := make([]SyncEntry, n)
	for i := range out {
		out[i] = SyncEntry{Path: fmt.Sprintf("%s/d%04d/...", syncStream, i), Rev: "#head"}
	}
	return out
}

// syncSpecOf wraps entries in a one-task job spec rooted at syncStream, so the
// only rule any case in this file can trip is a source rule.
func syncSpecOf(entries []SyncEntry) *JobSpec {
	return &JobSpec{
		Name: "sync-bounds",
		Tasks: []TaskSpec{{
			Name:    "t",
			Command: []string{"true"},
			Source: &SourceSpec{
				Type:   "perforce",
				Stream: syncStream,
				Sync:   entries,
			},
		}},
	}
}

// syncOverMsg is the refusal, as Validate's per-task wrapper prefixes it.
//
// THE NUMBERS ARE LITERALS, NOT maxSyncEntries, ON PURPOSE. A test that spells
// the constant agrees with the implementation by construction and cannot detect
// the constant moving, which is the single most likely defect in a change that
// is one constant and one comparison.
const syncOverMsg = "task t: at most 512 sync entries are allowed, got 513"

// TestValidate_TheSyncEntryCountIsBoundedAtBothEnds pins the range whose lower
// end - "source.sync must have at least one sync entry" - already existed, and
// whose upper end this bound adds.
func TestValidate_TheSyncEntryCountIsBoundedAtBothEnds(t *testing.T) {
	t.Run("an empty sync list is refused", func(t *testing.T) {
		require.EqualError(t, Validate(syncSpecOf(nil)),
			"task t: source.sync must have at least one sync entry",
			"the lower end of the range, pinned in the same file as the upper end so an edit that "+
				"deletes either one is caught beside the other")
	})

	t.Run("one over the cap is rejected and the message reports the count", func(t *testing.T) {
		require.EqualError(t, Validate(syncSpecOf(syncIncludes(513))), syncOverMsg,
			"the message must STATE the limit and REPORT what arrived: it is what the boot sweep "+
				"writes into scheduled_jobs.last_error and what run-now answers with, so a caller who "+
				"generated the spec has to be able to read by how much to cut it with no context around it")
	})

	t.Run("exactly at the cap is accepted", func(t *testing.T) {
		require.NoError(t, Validate(syncSpecOf(syncIncludes(512))),
			"a spec AT the boundary must still be accepted - this is the leg an off-by-one written "+
				"as >= breaks, and nothing else in the tree catches it")
	})

	t.Run("an ordinary source spec still validates", func(t *testing.T) {
		// CONTROL. Without it, a validateSourceSpec that had started refusing
		// everything would pass every refusal case in this file. It is in this
		// package deliberately: the sibling control in internal/api cannot see a
		// break confined to this one.
		require.NoError(t, Validate(syncSpecOf([]SyncEntry{
			{Path: "//s/Engine/...", Rev: "#head"},
			{Path: "//s/Content/...", Rev: "@1200"},
			{Path: "//s/Content/Movies/...", Exclude: true},
		})), "control: a three-entry spec that is legal on every rule must stay legal")
	})
}

// TestValidate_TheSyncEntryCountIsCheckedBeforeEveryPerEntryRule pins the
// placement. In each case the spec violates a LATER rule as well, so the
// returned message is the only observable trace of which check ran first - and
// running first is the whole point, since each later rule is work this bound
// exists to avoid doing on an over-count spec.
func TestValidate_TheSyncEntryCountIsCheckedBeforeEveryPerEntryRule(t *testing.T) {
	t.Run("before the per-entry loop", func(t *testing.T) {
		// The FIRST entry carries an invalid rev, so the per-entry loop fails on
		// iteration zero. Below the loop, this returns the rev message. Above it,
		// the spec never pays 513 prefix checks, control-byte scans and regexp
		// matches.
		entries := syncIncludes(513)
		entries[0].Rev = "garbage"
		require.EqualError(t, Validate(syncSpecOf(entries)), syncOverMsg)
	})

	t.Run("before the coverage loop", func(t *testing.T) {
		// 513 entries: 511 distinct includes, one include broadened to the whole
		// stream, and one exclusion that both the broad include and its own parent
		// include cover. Below the coverage loop this returns "covered by exactly
		// one included path, found 2". That loop is O(exclusions x len(Sync)) and
		// closing its second factor is the placement argument.
		entries := syncIncludes(512)
		entries[0].Path = syncStream + "/..."
		entries = append(entries, SyncEntry{Path: syncStream + "/d0001/y/...", Exclude: true})
		require.Len(t, entries, 513)
		require.EqualError(t, Validate(syncSpecOf(entries)), syncOverMsg)
	})

	t.Run("before the exclusion count", func(t *testing.T) {
		// Over BOTH source bounds: 513 entries of which 17 are exclusions. The
		// entry count wins, deliberately - it is knowable without traversing
		// anything, and it is the more actionable message for a spec that is over
		// both. Transpose the two checks and this returns "at most 16 excluded
		// sync paths are allowed, got 17".
		entries := syncIncludes(496)
		for i := 0; i < 17; i++ {
			entries = append(entries, SyncEntry{
				Path: fmt.Sprintf("%s/d%04d/x/...", syncStream, i), Exclude: true,
			})
		}
		require.Len(t, entries, 513)
		require.EqualError(t, Validate(syncSpecOf(entries)), syncOverMsg)
	})
}

// TestValidate_TheSyncEntryCountCountsEveryEntry pins the AXIS: the bound counts
// entries, not includes and not #head entries. Both cases below are legal on
// every other rule, so an implementation that counts the wrong subset accepts
// them while every other case in this file stays green.
func TestValidate_TheSyncEntryCountCountsEveryEntry(t *testing.T) {
	t.Run("an exclusion counts toward the total", func(t *testing.T) {
		// 512 includes plus one exclusion covered by exactly one of them and
		// swallowing nothing: 513 entries, legal on every rule but this one. An
		// implementation counting only includes sees 512 and accepts.
		entries := append(syncIncludes(512),
			SyncEntry{Path: syncStream + "/d0000/x/...", Exclude: true})
		require.Len(t, entries, 513)
		require.EqualError(t, Validate(syncSpecOf(entries)), syncOverMsg)
	})

	t.Run("a spec of pinned revisions is bounded too", func(t *testing.T) {
		// 513 entries, no #head anywhere. An implementation counting only the
		// entries that cost a p4 round trip sees 0 and accepts - which is what the
		// backlog item's own framing would have produced, and it would leave the
		// argv length and both BaselineHash sites unbounded.
		entries := syncIncludes(513)
		for i := range entries {
			entries[i].Rev = "@1200"
		}
		require.EqualError(t, Validate(syncSpecOf(entries)), syncOverMsg)
	})
}
```

- [ ] **Step 2: Add the helper and the two rows to `internal/api/job_spec_source_test.go`**

This proves the rule reaches the API layer's exported type aliases, which is a different property from the rule existing. **Two rows, not a copy of the file above.**

Append this helper immediately after `manySyncExclusions` (which currently ends at line 197):

```go
// manySyncIncludes returns n distinct, non-nesting include entries under the
// table's stream, so the only rule an over-count case can trip is the count.
func manySyncIncludes(n int) []SyncEntry {
	out := make([]SyncEntry, n)
	for i := range out {
		out[i] = SyncEntry{Path: fmt.Sprintf("//streams/X/main/d%04d/...", i), Rev: "#head"}
	}
	return out
}
```

`fmt` is already imported at line 4; do not add an import.

Insert these two rows immediately after the `"seventeen exclusions"` case (currently lines 151-153), so the two source count bounds sit together:

```go
		// The other source count bound, through the SAME type aliases: this
		// package declares SourceSpec and SyncEntry as aliases of the jobspec
		// types and ValidateJobSpec takes a value rather than a pointer, and
		// these rows prove the rule survives that surface at both sides of the
		// boundary.
		{"five hundred and thirteen sync entries", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = manySyncIncludes(513)
		}, "at most 512 sync entries are allowed, got 513"},
		{"five hundred and twelve sync entries is allowed", func(s *JobSpec) {
			s.Tasks[0].Source.Sync = manySyncIncludes(512)
		}, ""},
```

- [ ] **Step 3: Run the new tests and RECORD the RED**

```bash
go test ./internal/jobspec/ -run TestValidate_TheSyncEntry -v -count=1
go test ./internal/api/ -run TestValidateJobSpec_Source_Perforce -v -count=1
```

Expected, and **record each one - do not summarise as "it failed"**:

| Subtest | At HEAD |
|---|---|
| `TheSyncEntryCountIsBoundedAtBothEnds/an_empty_sync_list_is_refused` | **PASS** - the lower end already exists. This case has no RED and is not the headline; it earns its place by pinning the pair as one range. |
| `.../one_over_the_cap_is_rejected_and_the_message_reports_the_count` | **FAIL** - `Validate` returns nil; `require.EqualError` reports "An error is expected but got nil." |
| `.../exactly_at_the_cap_is_accepted` | **PASS** - 512 is accepted today. This is the `>=` mutant's killer, not a RED. |
| `.../an_ordinary_source_spec_still_validates` | **PASS** - the control. |
| `IsCheckedBeforeEveryPerEntryRule/before_the_per_entry_loop` | **FAIL** - got `task t: sync[0].rev: invalid rev "garbage"` |
| `.../before_the_coverage_loop` | **FAIL** - got `task t: sync[512]: excluded path //s/d0001/y/... must be covered by exactly one included path, found 2` |
| `.../before_the_exclusion_count` | **FAIL** - got `task t: at most 16 excluded sync paths are allowed, got 17` |
| `CountsEveryEntry/an_exclusion_counts_toward_the_total` | **FAIL** - nil |
| `CountsEveryEntry/a_spec_of_pinned_revisions_is_bounded_too` | **FAIL** - nil |
| api `five hundred and thirteen sync entries` | **FAIL** - no error |
| api `five hundred and twelve sync entries is allowed` | **PASS** |

**Six REDs in `internal/jobspec` plus one in `internal/api`. If you see a different count, or a RED with a message this table does not predict, stop and diagnose before implementing** - a fixture that trips an unintended rule is the "a test can be green because of the bug" shape, and this table exists to catch it.

---

## Task 2: The bound

**Files:**
- Modify: `internal/jobspec/jobspec.go` - insert a constant immediately before `maxSyncExclusions` (whose comment currently begins at line 299) and a check immediately after the empty-sync refusal (currently lines 550-552)

Nothing is committed in this task.

- [ ] **Step 1: Add the constant**

Insert immediately **before** the `// maxSyncExclusions bounds...` comment block, so the two source bounds sit in the order their checks fire:

```go
// maxSyncEntries bounds len(SourceSpec.Sync), counting includes and exclusions
// together. A relay sync list names subtrees, not files: one stream root is the
// common shape, a handful of named top-level directories is the next, and a
// generated per-asset or per-shot list is the outer edge at low hundreds. A spec
// wanting more is better served by naming a parent path. 512 is several times
// that outer edge.
//
// IT IS THE OTHER END OF AN EXISTING RANGE. validateSourceSpec already refuses
// an empty sync list, and this is that sentence's upper end.
//
// EVERY COST IT BOUNDS IS DRIVEN BY THE ENTRY COUNT, NOT BY ANY rev: one
// ResolveHead round trip per #head entry inside the task's own prepare phase,
// repeated on every attempt; one argv element per include on the single p4 sync;
// the second factor of the O(exclusions x len(Sync)) coverage and swallow loops
// below, which perforce.preemptSpecs walks again; and one BaselineHash per
// prepare and one per candidate worker scored by scheduler.selectWorker. A bound
// on the #head subset would leave all but the first of those open.
//
// IT DOES NOT BOUND ARGV BYTES. Path length is bounded by maxBodyBytes alone, so
// 512 long client paths is still a large command line and platform command-line
// limits are real. That is a pre-existing hazard this narrows and does not close.
//
// CHECKED BEFORE THE PER-ENTRY LOOP, so an over-count spec is refused without
// paying three prefix checks, a control-byte scan and up to four regexp matches
// per entry, and without the quadratic below. That places it above the
// excluded-count check, which needs the count that loop produces; a spec over
// both bounds therefore reports the entry count, which is the number knowable
// without work and the more actionable of the two.
//
// DO NOT RAISE THIS WITHOUT A REFUSED REAL SUBMISSION. "The number looks small"
// is not the reason. And 512 is deliberately not 500: maxCommandsPerTask above
// is the other concentration control on this quantity, the two are independent -
// one counts commands the agent executes, the other counts p4 round trips before
// any command runs - and a cap spelled identically would read as derived from it.
//
// DO NOT MAKE THIS ENV-CONFIGURABLE. See maxRetries above: the argument is about
// Validate running on STORED scheduled_jobs.job_spec rows, and it applies
// identically to every bound in this file.
const maxSyncEntries = 512
```

**Do not add a date, a change note, a count of anything elsewhere, a uniqueness claim about other code, or any sentence about how this was measured or reviewed.** RED/GREEN and mutation provenance go in the commit message.

**Do not add a reduction-factor sentence here.** That is Task 6, and it is blocked on Task 0 Step 1.

- [ ] **Step 2: Add the check**

In `validateSourceSpec`, immediately after the existing empty-sync refusal, insert:

```go
	if len(s.Sync) > maxSyncEntries {
		return fmt.Errorf("at most %d sync entries are allowed, got %d", maxSyncEntries, len(s.Sync))
	}
```

so the region reads:

```go
	if len(s.Sync) == 0 {
		return errors.New("source.sync must have at least one sync entry")
	}
	if len(s.Sync) > maxSyncEntries {
		return fmt.Errorf("at most %d sync entries are allowed, got %d", maxSyncEntries, len(s.Sync))
	}
	excluded := 0
	for i, e := range s.Sync {
```

`fmt` is already imported. Do not touch `excluded`, the per-entry loop, the exclusion-count check, the coverage loop, the unshelves loop or the client-template check.

- [ ] **Step 3: Run the tests and verify GREEN**

```bash
go test ./internal/jobspec/ -count=1 -v
go test ./internal/api/ -run TestValidateJobSpec_Source -count=1 -v
```

Expected: PASS on all nine `sync_bounds_test.go` subtests, PASS on both new API rows, and PASS on every pre-existing test in both packages including `TestValidateJobSpec_Source_Perforce`'s `happy path`, `sixteen exclusions is allowed` and `seventeen exclusions` rows, which must be **untouched and green**.

- [ ] **Step 4: Run the whole untagged suite**

```bash
go test ./... -count=1
```

Expected: identical to the Task 0 Step 4 baseline. Any package that changed from `ok` to FAIL is yours; any that was already red stays red and is not yours.

---

## Task 3: Mutation battery

The bound is one constant and one comparison, which is exactly the shape that survives a weak test. Nothing is committed in this task; the results go into Task 4's commit message.

**Two rules that are not optional on this project:**

- **Never revert a mutation with `git checkout -- internal/jobspec/jobspec.go`.** The file carries uncommitted work at this point (Task 2 is not committed yet) and `git checkout --` would discard the guard under test.
- **Verify every mutation actually applied.** A silently-unapplied or behaviourally inert mutation reports "survived" and looks like a hole in your tests.

- [ ] **Step 1: Take a restore copy OUTSIDE the repo**

PowerShell:

```powershell
New-Item -ItemType Directory -Force $env:TEMP\relay-lane-s | Out-Null
Copy-Item internal\jobspec\jobspec.go $env:TEMP\relay-lane-s\jobspec.go.orig -Force
```

Bash:

```bash
mkdir -p /tmp/relay-lane-s && cp internal/jobspec/jobspec.go /tmp/relay-lane-s/jobspec.go.orig
```

Restore after each mutation by copying that file back, never by a git command.

- [ ] **Step 2: Run a CONTROL mutation that must die, to prove the harness works**

Change the message text only - `"at most %d sync entries are allowed, got %d"` to `"at most %d sync entries are permitted, got %d"` - then:

```bash
git diff --stat internal/jobspec/jobspec.go
go test ./internal/jobspec/ -run TestValidate_TheSyncEntry -count=1
```

Expected: the diffstat shows one insertion and one deletion more than your Task 2 additions, and the test run FAILS on six subtests. **If it passes, your harness is broken - the edit did not apply, or you are running the wrong package. Stop and fix that before proceeding.** Restore from the copy.

- [ ] **Step 3: Run each mutation, confirm the diffstat, record which test died**

For each row: apply the mutation, run `git diff --stat internal/jobspec/jobspec.go` and confirm the line count matches the edit you intended, run `go test ./internal/jobspec/ ./internal/api/ -count=1 -v`, record the FAILING subtest names, then restore from the copy and confirm green again before the next row.

| # | Mutation | Test that MUST go RED |
|---|---|---|
| 1 | `len(s.Sync) > maxSyncEntries` -> `>=` | `TheSyncEntryCountIsBoundedAtBothEnds/exactly_at_the_cap_is_accepted`, and the api `five hundred and twelve sync entries is allowed` row |
| 2 | `const maxSyncEntries = 512` -> `513` | `.../one_over_the_cap_is_rejected_and_the_message_reports_the_count` |
| 3 | **Delete the check entirely** | six subtests: `one_over_the_cap...`, all three of `IsCheckedBeforeEveryPerEntryRule`, both of `CountsEveryEntry`, plus the api 513 row. `exactly_at_the_cap_is_accepted`, `an_empty_sync_list_is_refused` and the control must stay GREEN. |
| 4 | Move the check to immediately AFTER the per-entry loop (below the loop's closing brace, above the `excluded >` check) | `IsCheckedBeforeEveryPerEntryRule/before_the_per_entry_loop` |
| 5 | Move the check to immediately AFTER the `excluded > maxSyncExclusions` check | `IsCheckedBeforeEveryPerEntryRule/before_the_exclusion_count` (and, by construction, `before_the_per_entry_loop` too) |
| 6 | Move the check to immediately AFTER the coverage/swallow loop | `IsCheckedBeforeEveryPerEntryRule/before_the_coverage_loop` |
| 7 | Count only includes: replace the condition with a pre-count of `!e.Exclude` entries | `CountsEveryEntry/an_exclusion_counts_toward_the_total` |
| 8 | Count only `#head` entries: replace the condition with a pre-count of `e.Rev == "#head"` entries | `CountsEveryEntry/a_spec_of_pinned_revisions_is_bounded_too` (and, by construction, `an_exclusion_counts_toward_the_total`) |
| 9 | Drop the `len(s.Sync)` argument from the message: `"at most %d sync entries are allowed"` | `.../one_over_the_cap...` |

**Mutation 3 is the ceiling test's own control.** The success path alone is what an ABSENT bound also produces; row 3 is how you check that the refusal cases are not vacuous. If deleting the check leaves any of the six green, that subtest is not testing the bound - fix the test, do not proceed.

Mutations 7 and 8 need a real edit, not a comment change. Write mutation 7 as, for example:

```go
	includes := 0
	for _, e := range s.Sync {
		if !e.Exclude {
			includes++
		}
	}
	if includes > maxSyncEntries {
		return fmt.Errorf("at most %d sync entries are allowed, got %d", maxSyncEntries, includes)
	}
```

and mutation 8 the same shape with `if e.Rev == "#head"`. Confirm the diffstat is proportionate before running each.

- [ ] **Step 4: Record the outcome and restore**

**Record here:** for each of the nine rows, the exact failing subtest names, or the word **SURVIVED**.

- **A survivor is a stop.** Go back to Task 1, add the discriminating case, and re-run the whole battery. A mutation proof must leave a permanent test behind: the input that kills the mutant has to survive into `sync_bounds_test.go`, not exist only in your session.
- **A kill must name its guard.** A mutation can redden a test for a reason other than the property you think it pins. For each kill, confirm the failing assertion is the one the table predicts, not a collateral failure elsewhere.

Restore and verify:

```bash
cp /tmp/relay-lane-s/jobspec.go.orig internal/jobspec/jobspec.go
git diff --stat internal/jobspec/jobspec.go
go test ./internal/jobspec/ ./internal/api/ -count=1
```

(PowerShell: `Copy-Item $env:TEMP\relay-lane-s\jobspec.go.orig internal\jobspec\jobspec.go -Force`.)

Expected: the diffstat shows only your Task 2 additions, and both packages pass.

---

## Task 4: Commit the bound and its tests

The commit comes after the battery, not before, because this project puts RED/GREEN and mutation provenance in the commit message and amending under concurrent agents is not safe.

- [ ] **Step 1: CRLF and encoding hygiene on every touched path**

This is a CRLF repo and `git diff` and `git status` disagree by design, so neither alone is a check.

```bash
git status --porcelain
git diff --stat internal/jobspec/jobspec.go internal/api/job_spec_source_test.go
git ls-files --eol internal/jobspec/jobspec.go internal/api/job_spec_source_test.go
gofmt -l internal/jobspec/sync_bounds_test.go internal/jobspec/jobspec.go internal/api/job_spec_source_test.go
```

Assert, and record:

- The diffstat is proportionate to the change you intended: roughly 40 insertions in `jobspec.go` and 0 deletions, about 14 insertions in the api test. **A diffstat far larger than that means a file was reclassified or rewritten - stop and diagnose.**
- Every tracked path in `git ls-files --eol` reads `i/lf`. `sync_bounds_test.go` is new and will only appear after `git add`; re-run that command after staging.
- `gofmt -l` names none of the three. Note that `gofmt -l` over the whole tree is useless here - it lists hundreds of files purely because of working-copy CRLF - so scope it to your paths.
- **The three files still decode as UTF-8, and every byte in them is ASCII.** All content this plan specifies is ASCII; there is no accented character, no dash other than the hyphen-minus, and no non-ASCII literal anywhere. Verify:

```bash
python -c "import sys;[open(p,'rb').read().decode('utf-8') for p in sys.argv[1:]];print('utf-8 ok')" internal/jobspec/jobspec.go internal/jobspec/sync_bounds_test.go internal/api/job_spec_source_test.go
rg -n --pcre2 "[^\x00-\x7F]" internal/jobspec/jobspec.go internal/jobspec/sync_bounds_test.go internal/api/job_spec_source_test.go
```

Expected: `utf-8 ok`, and the second command prints nothing.

- [ ] **Step 2: Confirm the throwaway harness is gone**

```bash
git status --porcelain internal/api/
```

Expected: `internal/api/zz_measure_body_ceiling_test.go` does **not** appear. If it does, delete it now.

- [ ] **Step 3: Commit with an explicit pathspec**

Run this through the **Bash** tool, not PowerShell, so the multi-`-m` quoting is handled by the shell it was written for.

```bash
git add internal/jobspec/jobspec.go internal/jobspec/sync_bounds_test.go internal/api/job_spec_source_test.go
git ls-files --eol internal/jobspec/sync_bounds_test.go
git commit -m "feat(jobspec): bound source.sync at 512 entries" -m "validateSourceSpec bounded nothing about len(s.Sync). The same field's
excluded entries were capped at 16; its included entries were not capped at
all, so the field was closed on one axis and open on the other.

The bound is TOTAL - includes and exclusions together - because every cost it
stands in for is driven by the entry count rather than by any rev: one
ResolveHead round trip per #head entry per attempt, one argv element per include
on the single p4 sync, the second factor of the O(exclusions x len(Sync))
coverage and swallow loops here and in perforce.preemptSpecs, and one
BaselineHash per prepare plus one per candidate worker scored by selectWorker.
A bound on the #head subset would leave all but the first open.

The check sits immediately after the empty-sync refusal and above every
per-entry rule, so an over-count spec is refused without a per-entry prefix
scan, control-byte scan and regexp pass, and without the quadratic. That places
it above the excluded-count check, which needs the count that loop produces.

RETROACTIVE, no grandfathering, same as the count bounds. jobspec.Validate runs
on stored scheduled_jobs.job_spec rows, so a stored schedule carrying more than
512 sync entries stops firing on upgrade and carries the message in
scheduled_jobs.last_error from the first boot after this release; run-now
answers 400 with the same text and the PATCH clear-decision will not clear the
record until the stored spec is fixed. Grandfathering would need a second, more
permissive vocabulary for stored specs, which contradicts the single job-spec
pipeline invariant.

RED at HEAD, 6 of 9 new subtests plus the new api 513 row: one_over_the_cap
(nil), before_the_per_entry_loop (rev message), before_the_coverage_loop
(coverage message), before_the_exclusion_count (exclusion-count message),
an_exclusion_counts_toward_the_total (nil), a_spec_of_pinned_revisions (nil).
GREEN after. The three that were green at HEAD - an_empty_sync_list_is_refused,
exactly_at_the_cap_is_accepted and the control - are the range's lower end, the
>= killer and the refuses-everything guard.

Mutations run, each verified applied by diffstat, each restored from a copy
outside the repo rather than by git checkout:
  >= for >                     -> exactly_at_the_cap_is_accepted
  512 -> 513                   -> one_over_the_cap
  check deleted                -> six subtests; the three above stayed green
  moved below per-entry loop   -> before_the_per_entry_loop
  moved below exclusion count  -> before_the_exclusion_count
  moved below coverage loop    -> before_the_coverage_loop
  counting only includes       -> an_exclusion_counts_toward_the_total
  counting only #head entries  -> a_spec_of_pinned_revisions_is_bounded_too
  got-argument dropped         -> one_over_the_cap
Control mutation (message reworded) died first, so the harness was known good.

Untagged on purpose: Validate is a pure function, so the guards need no tag, no
database and no new CI job.

Closes: idea-2026-09-04-source-sync-has-no-entry-count-bound" -- internal/jobspec/jobspec.go internal/jobspec/sync_bounds_test.go internal/api/job_spec_source_test.go
```

**Replace the mutation lines with what you actually recorded in Task 3.** If any row survived and you strengthened a test, say which test you added and why the original was blind.

---

## Task 5: README

**Files:**
- Modify: `README.md`, the `sync` row of the Source workspaces field table (currently line 589)

**Exactly one row. Sibling lanes may also be editing README, so keeping the edit to one row keeps the conflict surface at one row.** The `tasks[].retries` row (currently line 774) was examined and **declined**: it is an argument about the command cost model, it is already silent about `unshelves` (which is also multiplied by attempts and also unbounded), and adding a fourth number would not make its list complete. Do not touch it.

- [ ] **Step 1: Replace the row**

Find the line beginning ``| `sync` | Yes | One or more paths to sync.`` and replace **only the first sentence**, leaving everything from `Each entry has` onward byte-identical. The whole row becomes:

```
| `sync` | Yes | One or more paths to sync, at most `512` entries; a longer list is rejected at submission, and re-checked every time a stored schedule fires - see [Scheduled jobs](#scheduled-jobs), because that makes the bound retroactive over schedules created by earlier releases. Every `#head` entry costs one round trip to the Perforce server inside the task's own prepare phase, and every attempt repeats them, so a broad path costs less than an enumerated list of its children. Each entry has `path` (depot path or `...`) and `rev` (`"#head"`, `@CL`, or `@label`). An entry may instead set `"exclude": true`, which leaves that path out of the sync; an excluded entry carries no `rev` (it is applied at the revision of the include that covers it) and must be covered by exactly one included path. At most 16 exclusions per spec. A relay-server that does not know the field ignores it and syncs the whole path. |
```

The retroactivity clause is lifted verbatim from the `tasks` row (line 767) and the `timeout_seconds` row (line 773), so the four bounds read identically - verified word for word against both. The cost sentence is there because a cap with no stated mechanism gets read as arbitrary and raised on the first complaint. **No reduction factor appears here and none may be added** - that sentence is blocked on Task 0 Step 1 and belongs in the code comment, not in README.

- [ ] **Step 2: Hygiene, then commit**

```bash
git diff --stat README.md
git ls-files --eol README.md
rg -c "at most .512. entries" README.md
python -c "open('README.md','rb').read().decode('utf-8');print('utf-8 ok')"
```

Expected, and **assert each**: `1 file changed, 1 insertion(+), 1 deletion(-)`; `i/lf`; exactly `1` hit for the new phrase; `utf-8 ok`.

**A diffstat of more than one insertion and one deletion means the edit did not land on an exact anchor.** Revert with `git checkout -- README.md` (safe here - nothing else in README is yours and uncommitted at this point) and redo it with an exact-anchor replacement.

README already contains non-ASCII characters on other lines (the `type` row at line 587 uses an em dash), so an ASCII-only assertion over the whole file is not a valid check here; the `utf-8 ok` decode plus the one-line diffstat are. **Your own inserted text must contain no dash other than the hyphen-minus.**

```bash
git commit -m "docs(readme): state the 512-entry cap on source.sync" -m "One row: the sync row of the Source workspaces field table, which already
carries the exclusion cap, so the entry cap belongs beside it. The
retroactivity clause is the same sentence the tasks and timeout_seconds rows
carry, so the four bounds read identically. The cost sentence is there because
a cap with no stated mechanism gets read as arbitrary.

The tasks[].retries row was examined and declined: it is an argument about the
command cost model, it is already silent about unshelves, and a fourth number
would not make its list complete." -- README.md
```

---

## Task 6: The binding paragraph, from measured numbers only

**Files:**
- Modify: `internal/jobspec/jobspec.go`, `maxSyncEntries`'s comment

**THIS TASK IS CONDITIONAL. If Task 0 Step 1 did not produce `C_min` and `C_head`, SKIP IT ENTIRELY and say so in the PR body. Do not estimate, do not reuse the spec's "order of 37,000", do not write the paragraph with a hedge.** The spec is explicit that both the hand count and the division are unverified arithmetic in a document.

- [ ] **Step 1: Append the paragraph**

Insert immediately after the "IT IS THE OTHER END OF AN EXISTING RANGE" paragraph and before "EVERY COST IT BOUNDS":

```go
// IT STILL BINDS. maxBodyBytes (1 MiB, internal/api/server.go) caps how many
// entries one request can carry: the cheapest entry the decoder accepts is
// <E_MIN> bytes and puts <C_MIN> of them in one body, and the cheapest entry
// that also costs a p4 round trip is <E_HEAD> bytes and puts <C_HEAD>, so this
// cap is roughly a <FACTOR>x reduction on the per-task worst case. THE TWO
// NUMBERS BOUND DIFFERENT AXES and must not be collapsed: the first bounds the
// entry count, the second bounds the round trips.
```

Substitute the four values you recorded, with no angle brackets left anywhere:

- `<E_MIN>` and `<C_MIN>`: the `entryBytes` and `acceptedEntries` the harness printed for `cheapest entry the decoder accepts`.
- `<E_HEAD>` and `<C_HEAD>`: the same two for `cheapest entry that costs a p4 round trip`.
- `<FACTOR>`: `C_HEAD / 512`, rounded to a whole number, stated as "roughly".

**Worked example of the SHAPE ONLY - these are NOT the values to write, and if your measurement produces them it is a coincidence you must still have measured:** if the harness printed `entryBytes=25 acceptedEntries=41000` and `entryBytes=28 acceptedEntries=37000`, the sentence would read "the cheapest entry the decoder accepts is 25 bytes and puts 41000 of them in one body, and the cheapest entry that also costs a p4 round trip is 28 bytes and puts 37000, so this cap is roughly a 72x reduction".

Do **not** write the measurement procedure, the date, or who ran it. That is provenance, and it goes in the commit message.

- [ ] **Step 2: Verify and commit**

```bash
rg -n "E_MIN|C_MIN|E_HEAD|C_HEAD|FACTOR" internal/jobspec/jobspec.go
go build ./... && go test ./internal/jobspec/ -count=1
git diff --stat internal/jobspec/jobspec.go
git ls-files --eol internal/jobspec/jobspec.go
```

Expected: the first command prints nothing (no placeholder survived), the build and tests pass, the diffstat shows 7 insertions and 0 deletions, and the eol reads `i/lf`.

```bash
git commit -m "docs(jobspec): record the measured body ceiling on maxSyncEntries" -m "The reduction factor was deliberately withheld from the original commit
because it had not been measured. It was established by constructing a maximal
JSON body under maxBodyBytes for each of the two cheapest legal entry shapes and
counting what readJSON's decoder accepted, rather than by dividing 1 MiB by a
hand-counted entry length.

The two shapes are not the same entry and the design's own text conflated them:
the cheapest entry the decoder accepts pins its rev with two characters, while
the cheapest entry that also costs a p4 round trip spells #head. They bound
different axes and the comment now says so." -- internal/jobspec/jobspec.go
```

---

## Task 7: Final gates and the PR

- [ ] **Step 1: Full untagged suite and vet**

```bash
go build ./... && go vet ./... && go test ./... -count=1
```

Compare against the Task 0 Step 4 baseline package by package. **Record: which packages are green, and for any red one, whether it was red in the baseline.** A package red in both is pre-existing and is not yours; say so in exactly those words rather than calling it a flake. A single green re-run bounds a failure, it does not retire it.

- [ ] **Step 2: Race detector, in the container**

The Windows native `-race` lane is unreliable on this machine (two distinct failure modes, one of them a ThreadSanitizer shadow-arena allocation failure unattached to any test). The container is the reliable route and is also the only local way to run the `//go:build !windows` files at all:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

**`internal/testsupport/pgdsn` has a guard that is RED on a clean tree inside this container. It belongs to LANE E. Do not fix it, do not report it as caused by this slice** - name it as a known, other-lane failure and move on.

**If the container is genuinely unavailable, say plainly that `-race` did not run.** Do not substitute `-count=N` repetition: it re-runs under the ordinary scheduler and cannot observe an unsynchronised access that never interleaves badly. It raises confidence in flakiness, not in race-freedom. (This slice adds no concurrency, so a clean run is the expected result rather than new information; that is still not a reason to skip it and claim it passed.)

- [ ] **Step 3: Confirm the scope fence held**

```bash
git diff --stat origin/main...HEAD
```

Expected: exactly four files - `internal/jobspec/jobspec.go`, `internal/jobspec/sync_bounds_test.go`, `internal/api/job_spec_source_test.go`, `README.md` - plus the spec doc and this plan doc if the conductor committed them on this branch. **Any other path is a scope-fence breach: report it prominently rather than quietly reverting it, because under concurrency it may be a sibling lane's work that landed in your index.** Read the reflog before any reset.

- [ ] **Step 4: Write the PR body**

Use `gh pr create --body-file` with a file in your scratchpad; a long inline `--body` heredoc trips the classifier.

The body must carry, as required scope rather than as a nicety:

1. **The retroactivity release note.** In the form the count-bounds PR used: a schedule created before this release whose spec carries more than 512 sync entries stops firing on upgrade, carries `task <name>: at most 512 sync entries are allowed, got N` in `scheduled_jobs.last_error` from the first boot after the release, answers 400 on run-now, and will not have its failure record cleared by a PATCH until the stored spec is fixed. **Add an instruction to the operator to check their own deployment for such schedules.** How many exist in any real deployment cannot be established from this repository and must not be asserted here.
2. **Task 0's four measurements**, each with its recorded number and its command, and an explicit statement for any that was not taken.
3. **The mutation table**, as run, with the surviving-mutant count (which must be zero).
4. **What this bound does NOT do**, so the PR cannot be read as claiming more than it does:
   - It does not bound argv BYTES; path length is bounded by `maxBodyBytes` alone. Cite Task 0 Step 2's number and its verdict, and state that no exec was run at that boundary.
   - It does not close `source.unshelves`, which is the other unbounded per-entry subprocess axis on the same spec and by its own item's measurement the denser one. `bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded` stays OPEN and must not be marked as addressed.
   - It does not reduce the per-request aggregate; that stays bounded by `maxBodyBytes` alone and repetition stays bounded by `RELAY_JOB_SUBMIT_RATE_LIMIT` alone. This is a concentration control in the `maxCommandsPerTask` sense and nothing else.
   - It does not batch `ResolveHead`, does not touch `internal/scheduler/dispatch.go`, and does not reduce the bytes the dispatcher re-parses per tick.
5. **A statement that no row was added to `internal/schedrunner/stored_spec_count_bounds_test.go`**, with the one-sentence reason, so a reviewer does not read the absence as an oversight.

- [ ] **Step 5: Hand the backlog proposals to the conductor**

You do not file these. Name them in your final report so the conductor runs `/backlog`:

1. `BaselineHashFromAPISpec` is called inside `selectWorker`'s worker loop and is invariant in the worker being scored; `warmKey` one branch above is already hoisted with a comment explaining why. `internal/scheduler/dispatch.go`.
2. `SyncStream`'s argv length is bounded by nothing - one element per include, path length capped only by the request body. Carry Task 0 Step 2's measured number into the item. `internal/agent/source/perforce/client.go`.
3. `Sync`'s docstring in `python/src/relay/models.py` enumerates the server-side sibling rules by name and is one short once this lands. A one-clause generalisation, no number copied.
4. `bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded` should be re-read against this slice, not closed by it: this design corrects the sibling item's premise that the sync axis was the only unbounded one.

---

## Self-review

**Spec coverage.** Section 3 (total axis) -> Task 2 Step 2 plus `CountsEveryEntry`'s two cases. Section 4 (the number 512, and the withheld reduction factor) -> Task 2 Step 1 and Task 6. Section 5 (placement, and the precedence consequence) -> Task 2 Step 2 and `IsCheckedBeforeEveryPerEntryRule`'s three cases, with the complement claim discharged by Task 0 Step 3. Section 6 (the message) -> `syncOverMsg`, spelled as a literal. Section 7 (retroactivity) -> the "which retroactive paths get a guard" table and Task 7 Step 4 item 1. Section 8 (no Python, no SPA) -> the slice independence declaration; nothing to do. Section 9 (README) -> Task 5, including the declined `retries` row. Section 10 (what it does not do) -> Task 7 Step 4 item 4, with the planner's arithmetic correction recorded above. Section 11 (threat model) -> no code; the refusal increments no counter and names no knob, which is why no counter or remedy-ladder work appears anywhere in this plan. Section 12 (test plan) -> Tasks 1 and 3, with one addition (a same-package control) and one clarification (two api rows, not one, so the api table pins the boundary as well as the refusal). Section 13 (measurements) -> Task 0. Section 14 (scope fence) -> the scope fence section. Section 15 (backlog) -> Task 7 Step 5.

**Placeholder scan.** The only angle-bracket tokens in this plan are in Task 6, they are explicitly a fill-from-measurement form rather than a TBD, and Task 6 Step 2 has an `rg` command that fails the task if any survives. Every other code step carries complete, compilable content.

**Type consistency.** `syncStream`, `syncIncludes`, `syncSpecOf` and `syncOverMsg` are used with those exact spellings in all three test functions and collide with no existing identifier in package `jobspec` (checked against a `^func ` census of `count_bounds_test.go`, `jobspec_bounds_test.go`, `jobspec_test.go` and `jobspec.go`, whose helpers are `argvN`, `nTaskSpec`, `commandCountSpec`, `totalSpec`, `i32`, `twoTaskSpec`, `offenderSecondSpec`, `outOfRangeCases`). `manySyncIncludes` collides with nothing in package `api`, whose only sync helper is `manySyncExclusions`. `maxSyncEntries` is spelled identically in the constant, the check, and every mutation row.
