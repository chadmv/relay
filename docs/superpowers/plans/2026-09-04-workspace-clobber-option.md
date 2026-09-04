# Workspace `clobber` Option Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in, agent-level `RELAY_WORKSPACE_CLOBBER` that rewrites the `Options:` token list of a relay-created p4 client spec so `noclobber` becomes `clobber`, leaving every other field byte-identical and doing nothing at all when off.

**Architecture:** One env var parsed in `cmd/relay-agent/main.go`, carried on `perforce.Config`, passed by `Provider.Prepare` into `Client.CreateStreamClient`, applied by a new anchored line reader plus a token transform whose write goes back through the existing `setSpecField`. No proto field, no jobspec field, no REST parameter - a job spec cannot reach this knob, which is the point (spec D7).

**Tech Stack:** Go, `regexp`, `strconv.ParseBool`, `testify/require`, testcontainers-go p4d for the one integration observation.

**Spec:** `docs/superpowers/specs/2026-09-04-workspace-clobber-option.md`
**Item:** `docs/backlog/feature-2026-09-03-workspace-clobber-option.md`

---

## Slice independence declaration

**This is BACKEND + DOCS ONLY. One lane. No frontend slice exists.**

Nothing under `web/` changes. There is no API surface, no proto change, no generated code, no `.sql` file, and therefore no `make generate` step. Phase 3 runs a **single lane** owned by **`relay-backend-engineer`**, executing Tasks 1-8 in order. There is nothing to parallelise and no cross-slice dependency to sequence.

This is a single-PR slice. It does **not** need `/backlog phases`.

---

## Plan-time verification of the spec

The spec refuted seven item claims. Each of its own load-bearing corrections was re-read off the tree at HEAD before this plan was written.

| Spec claim | Verified against | Verdict |
|---|---|---|
| `setSpecField` replaces a whole line via a multiline regex, inserts at the TOP when absent, and writes a tab | `internal/agent/source/perforce/client.go:280-289` - `(?m)^%s:.*$`, `re.ReplaceAll` on match, else `fmt.Fprintf(&out, "%s:\t%s\n", ...)` then `out.Write(spec)` | **CONFIRMED, all three parts** |
| No bool env parser exists in `cmd/relay-agent` | `strconv.ParseBool` appears only at `cmd/relay-server/main.go:160` and `:190`, both `log.Fatalf` on error | **CONFIRMED** |
| The agent's idiom is warn-and-fall-back | `parseDurationEnv` (`cmd/relay-agent/main.go:202`) and `resolveSyncHeartbeatInterval` (`:178`) both warn and fall back | **CONFIRMED** |
| `RELAY_WORKSPACE_MIN_FREE_GB` discards its parse error entirely | `cmd/relay-agent/main.go:92` - `minFreeGB, _ := strconv.ParseInt(...)` | **CONFIRMED** |
| A live fixture already carries `Options: clobber` | `client_test.go:34` (`TestClient_CreateStreamClient_WithTemplate`) and `client_test.go:60` (`TestClient_CreateStreamClient_DropsAltRoots`) | **CONFIRMED** |
| `TestClient_CreateStreamClient_Default`'s fixture has no `Options:` line | `client_test.go:13-18` | **CONFIRMED** - this is why D2 is not hypothetical |
| `SubmitOptions:` is a real client-spec field | `docs/superpowers/specs/2026-04-24-perforce-workspace-management-design.md:113` names it beside `Options`; `docs/backlog/bug-2026-09-04-a-repaired-p4-client-loses-its-template-fields.md:18` names it | **CONFIRMED as an in-repo documented field** (not observed from live p4 output - Task 8 is where that gap closes) |
| `CreateStreamClient` now runs on every `Prepare` | `perforce.go:340` is unconditional; `TestProvider_AWarmPrepareStillRewritesTheClientSpec` (`perforce_warm_test.go:313`) pins it | **CONFIRMED** |
| D1 token equality, never prefix stripping | `noallwrite`/`allwrite` is a real pair; a `strings.TrimPrefix(tok, "no")` sweep flips it | **CONFIRMED as sound reasoning.** The plan encodes it as exact `==` comparisons only |
| F9's `classifyP4Error` is a first-match switch | `internal/agent/source/perforce/diagnostics.go:30-55` | **CONFIRMED** - deferred, see "Do not implement" below |

### What this plan REFUTES or corrects in the spec

1. **"`log` is already imported in this package (`sweeper.go`), so the two warnings need no new dependency" is true about the module and misleading about the edit.** Go imports are per-file. `internal/agent/source/perforce/client.go` imports `bufio, bytes, context, fmt, io, os/exec, regexp, strconv, strings` - **not `log`**. Task 2 must add `"log"` to `client.go`'s own import block or it will not compile. `sweeper.go` is the only file in the package that imports `log`.

2. **The spec's design misses one input class: an `Options:` line with an EMPTY value.** Section 2 says "If any token is not `[A-Za-z]+`, leave untouched and warn ... Otherwise append `clobber`." A bare `Options:` line yields zero tokens, so it passes the token-shape check vacuously and falls to "append", producing `Options:\tclobber` - a one-token option set, which is exactly the outcome D2 exists to forbid. **This plan treats zero tokens as malformed**: leave untouched, warn. Task 4 pins it.

3. **The spec does not notice that OFF is already partially pinned by an existing test.** `TestClient_CreateStreamClient_DropsAltRoots` asserts `require.Contains(t, spec, "Options: clobber")` with a **space** separator (`client_test.go:71`). Any implementation that routes that fixture's `Options:` line through `setSpecField` re-renders it as `Options:\tclobber` and turns that assertion RED. This is a free, pre-existing guard against an unconditional transform, and Task 3's mutation step exploits it.

4. **F5's `SubmitOptions:` claim is documented in-repo, not observed.** Two design/backlog docs name the field; no fixture or captured p4 output in the tree contains one. The anchoring requirement is correct regardless - `strings.Contains(line, "Options:")` would also match a `Description:` line containing the word - but the plan does not present F5 as measured.

Everything else in the spec survived.

### Do not implement

- **The `classifyP4Error` case for "can't clobber writable file" (item Proposal 4).** DEFERRED per spec F9/D5, on the open `docs/backlog/bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr.md`. Do not add a case to `diagnostics.go`. Do not name `RELAY_WORKSPACE_CLOBBER` in any error remedy string.
- **A forced `noclobber` when the option is off** (the item's own Tests sentence). Refuted by spec D6: OFF makes no call at all.
- **A wiring guard for `cmd/relay-agent`** (item Proposal 1). Spec D3: that guard does not exist for this binary and building it is a different item.
- **Any extension of `setSpecField`.** Spec D1: it has four other call sites that want line granularity.

---

## The two vacuity traps, stated once and enforced in every task

Both come from the spec and both silently produce a green test that proves nothing.

**Trap 1 - substring assertions.** `require.Contains(spec, "clobber")` is satisfied by `noclobber`. `require.NotContains(spec, "noclobber")` is satisfied by a spec with no `Options:` line at all. **Every assertion about the result must compare the `Options:` line's TOKEN LIST**, parsed out of the written spec, against an expected `[]string`. A test in this slice that reaches for `require.Contains` on the word `clobber` is a defect regardless of whether it passes.

**Trap 2 - poison placed after the subject.** The item's headline test is "a template whose `Description:` contains `noclobber` is untouched outside `Options:`". The mutant it exists to kill is the fork's `bytes.Replace(spec, "noclobber", "clobber", 1)`. That mutant only dies if the poisoned `Description:` line appears **BEFORE** the `Options:` line in the fixture. With the poison after, the first occurrence is the real one, the mutant edits the right field, and the test passes on the broken implementation. Fixture ordering is the test.

---

## File structure

| File | Responsibility | Change |
|---|---|---|
| `internal/agent/source/perforce/client.go` | p4 CLI invocations and spec-form editing | Add `clobber bool` param to `CreateStreamClient`; add `optionsLineRe`, `optionsTokens`, `withClobberOption`; add `"log"` import |
| `internal/agent/source/perforce/client_test.go` | Client-level unit tests | Add four tests; update three existing call sites for the new param |
| `internal/agent/source/perforce/perforce.go` | Provider lifecycle | Add `Config.Clobber`; pass `p.cfg.Clobber` at the single call site (line 340) |
| `internal/agent/source/perforce/perforce_warm_test.go` | Provider-level Prepare tests | Add the `Config`-field-to-spec-bytes test |
| `cmd/relay-agent/main.go` | Agent entrypoint and env parsing | Add `parseBoolEnv`; add `Clobber:` to the `perforce.Config` literal |
| `cmd/relay-agent/main_test.go` | Env parser tests | Add `TestParseBoolEnv` |
| `README.md` | Operator docs | One agent env-table row |

No `.sql`, no `.proto`, no generated file. **Do not run `make generate`** in this slice.

---

### Task 1: Widen the signature, ignore the parameter

Mechanical only. This task exists so that Task 2's RED is a **failed assertion**, not a compile error - a compile error cannot distinguish "not implemented" from "implemented wrong" (spec R1a).

**Files:**
- Modify: `internal/agent/source/perforce/client.go:130` (signature only)
- Modify: `internal/agent/source/perforce/perforce.go:340` (the one production call site)
- Modify: `internal/agent/source/perforce/client_test.go:21,39,64` (three test call sites)

- [ ] **Step 1: Widen the signature and leave the body alone**

In `client.go`, change the signature and its doc comment's last clause:

```go
// CreateStreamClient creates (or recreates) a stream-bound p4 client.
// If template is non-empty, uses -t <template> to inherit non-View fields.
// clobber, when true, rewrites the fetched spec's Options: token list so a
// writable unopened file cannot wedge every later sync on this workspace;
// when false nothing is written to Options: at all.
func (c *Client) CreateStreamClient(ctx context.Context, name, root, stream, template string, clobber bool) error {
```

Add nothing to the body yet. Go permits an unused function parameter, so this compiles.

The `bool` sits after four `string`s, so the same-typed-adjacent-argument transposition hazard does not apply - the compiler rejects a swap.

- [ ] **Step 2: Update the four call sites**

`perforce.go:340` becomes `p.cfg.Client.CreateStreamClient(ctx, clientName, wsRoot, pf.Stream, tmpl, false)`. The three `client_test.go` calls each gain a trailing `false`.

- [ ] **Step 3: Verify the package still builds and every existing test passes**

Run: `go test ./internal/agent/source/perforce/... -count=1 -timeout 120s`
Expected: `ok`. No test changed behaviour.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/perforce.go internal/agent/source/perforce/client_test.go
git commit -m "refactor: widen CreateStreamClient with an ignored clobber parameter"
```

---

### Task 2: The headline test - a `Description:` containing `noclobber` is untouched

This is the test the item names as carrying the field-edit requirement, and the item says it is written first. It is.

**Files:**
- Test: `internal/agent/source/perforce/client_test.go` (new `TestClient_CreateStreamClient_ClobberEditsOnlyTheOptionsToken`)
- Modify: `internal/agent/source/perforce/client.go`

- [ ] **Step 1: Add a token-list helper to the test file**

Add one unexported helper in `client_test.go` that, given the spec string written to `client -i`, finds the line beginning `Options:` (anchored at line start, so `SubmitOptions:` cannot match) and returns `strings.Fields` of the remainder. Name it `optionsTokensOf`. Every assertion in Tasks 2-4 goes through it. This is what makes Trap 1 unreachable: there is no substring check to write.

- [ ] **Step 2: Write the failing test**

Name: `TestClient_CreateStreamClient_ClobberEditsOnlyTheOptionsToken`.

**The fixture text is load-bearing and must be used exactly as written, with the `Description:` block BEFORE the `Options:` line.** Register it under the key `client -o -S //streams/X/main relay_h_abc` on a `newFakeP4Fixture(t)`, plus the usual `fr.set("client -i", "Client saved.\n")`:

```
Client: relay_h_abc
Description:
	build farm template - do not set noclobber here
Root: D:\somewhere\else
Options:	noallwrite noclobber nocompress unlocked nomodtime normdir
View: //streams/X/main/... //relay_h_abc/...
```

Call `c.CreateStreamClient(ctx, "relay_h_abc", ` + "`D:\\rw\\abcdef`" + `, "//streams/X/main", "", true)`.

Both assertions are required; either alone is vacuous:

1. **The `Description:` block is byte-identical to the fetched spec.** Assert the exact line `	build farm template - do not set noclobber here` (leading tab) is present in `fr.calls[1].stdin`, and that the fetched spec's word `noclobber` inside it survived. A one-sided "Description unchanged" assertion passes a no-op implementation, which is why assertion 2 exists.
2. **The written `Options:` token list is exactly** `[]string{"noallwrite", "clobber", "nocompress", "unlocked", "nomodtime", "normdir"}` - compared with `require.Equal` on the slice from `optionsTokensOf`. Order and arity are both asserted. `noallwrite` is in position 0 deliberately: it is what a `TrimPrefix(tok, "no")` implementation flips to `allwrite`, and comparing the whole slice is what catches that (spec D1).

Add a test comment stating the property pinned and why the fixture ordering discriminates - the poison must precede the subject or a first-occurrence byte replacement survives.

- [ ] **Step 3: Run it and confirm the RED is an assertion, not a compile error**

Run: `go test ./internal/agent/source/perforce/... -run TestClient_CreateStreamClient_ClobberEditsOnlyTheOptionsToken -count=1 -v -timeout 60s`

Expected: `--- FAIL`, from assertion 2, reporting that the actual token list still contains `noclobber` where `clobber` was expected. **If you instead see a build failure, Task 1 was not applied - stop and fix that first.**

- [ ] **Step 4: Write the minimal implementation**

In `client.go`, add `"log"` to the import block (it is not there today - see the plan-time correction above), then add above `setSpecField`:

```go
// optionsLineRe is anchored at line start for the same reason setSpecField's
// own pattern is: a client spec carries SubmitOptions: as well as Options:,
// and an unanchored match rewrites the wrong field.
var optionsLineRe = regexp.MustCompile(`(?m)^Options:.*$`)

// withClobberOption turns the Options: token noclobber into clobber, leaving
// every other line untouched. It edits tokens that are present and asserts
// nothing about their count or order, because no fixture in this repo shows a
// real Options: line and p4's default set is an assumption here, not a fact.
func withClobberOption(spec []byte, clientName string) []byte {
	line := optionsLineRe.Find(spec)
	if line == nil {
		log.Printf("warning: RELAY_WORKSPACE_CLOBBER is set but client %q has no Options: line; leaving the spec unchanged", clientName)
		return spec
	}
	toks := strings.Fields(strings.TrimPrefix(string(line), "Options:"))
	for i, t := range toks {
		if t == "noclobber" {
			toks[i] = "clobber"
		}
	}
	return setSpecField(spec, "Options", strings.Join(toks, " "))
}
```

This is deliberately minimal: it does not yet append when neither token is present, and it does not yet validate token shape. Task 4 drives both of those with their own REDs.

Wire it into `CreateStreamClient`, immediately after the `removeSpecBlock(spec, "AltRoots")` line and before the `client -i` run:

```go
	if clobber {
		spec = withClobberOption(spec, name)
	}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/agent/source/perforce/... -run TestClient_CreateStreamClient_ -count=1 -v -timeout 60s`
Expected: `--- PASS` for the new test and for all four existing `TestClient_CreateStreamClient_*` tests.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/client_test.go
git commit -m "feat: rewrite the p4 client spec's Options: noclobber token when clobber is requested"
```

---

### Task 3: OFF is a no-op, proven on a template that already sets `clobber`

Cannot go RED at HEAD-of-Task-2: the OFF path is already a no-op by construction. **Name the mutation instead** (Step 3).

**Files:**
- Test: `internal/agent/source/perforce/client_test.go` (new `TestClient_CreateStreamClient_ClobberOffLeavesTheOptionsLineByteIdentical`)

- [ ] **Step 1: Write the test**

Name: `TestClient_CreateStreamClient_ClobberOffLeavesTheOptionsLineByteIdentical`. Table-driven with two rows, both calling `CreateStreamClient(..., "", false)`:

| Row | Fetched `Options:` line | Required assertion |
|---|---|---|
| `p4 default` | `Options:	noallwrite noclobber nocompress unlocked nomodtime normdir` | the written spec contains that line **byte-identical, tab included** |
| `operator template already sets clobber` | `Options: clobber` (single space, exactly as `client_test.go:34` models) | the written spec contains that line **byte-identical, space included** |

The byte-identity assertion is what makes this stronger than a token comparison here: it also pins that `setSpecField` was never called for `Options`, since `setSpecField` normalises the separator to a tab and the separators in the two rows differ from each other.

The second row is the one that refutes the item's own "write `noclobber` when disabled" test sentence (spec D6): forcing `noclobber` here would destroy an operator's deliberate template setting.

- [ ] **Step 2: Run it**

Run: `go test ./internal/agent/source/perforce/... -run TestClient_CreateStreamClient_ClobberOff -count=1 -v -timeout 60s`
Expected: `--- PASS` on both subtests, first run. This test is a regression guard, not a red-first step.

- [ ] **Step 3: Prove it discriminates - run the mutation**

**Do not use `git checkout --` to revert this mutation** (it would discard the uncommitted test). Copy `client.go` aside first, or undo by hand.

Mutation: delete the `if clobber {` guard in `CreateStreamClient`, applying `withClobberOption` unconditionally.

Run: `go test ./internal/agent/source/perforce/... -run TestClient_CreateStreamClient_ -count=1 -v -timeout 60s`

Expected kills, and name which guard each one traces to:
- `TestClient_CreateStreamClient_ClobberOffLeavesTheOptionsLineByteIdentical/p4_default` - RED, the separator became a tab-joined single-space list and `noclobber` became `clobber`.
- `TestClient_CreateStreamClient_ClobberOffLeavesTheOptionsLineByteIdentical/operator_template_already_sets_clobber` - RED, `Options: clobber` was re-rendered as `Options:\tclobber`.
- `TestClient_CreateStreamClient_DropsAltRoots` - RED at `client_test.go:71`, the pre-existing `require.Contains(t, spec, "Options: clobber")` with a space. This is a free kill from a test written for a different purpose; record it, because it means the OFF property has two independent guards.

Restore `client.go` and re-run the same command as a control. Expected: `--- PASS` on all of them. **A uniform result across all three is a broken harness, not three kills** - if nothing went red, verify the mutation actually applied.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/source/perforce/client_test.go
git commit -m "test: pin that clobber=false leaves the Options: line byte-identical"
```

---

### Task 4: The three remaining branches - missing line, no matching token, malformed value

Three genuine REDs against Task 2's minimal implementation, plus one mutation for the anchoring.

**Files:**
- Test: `internal/agent/source/perforce/client_test.go`
- Modify: `internal/agent/source/perforce/client.go` (`withClobberOption`)

- [ ] **Step 1: Write the three failing tests**

All three call `CreateStreamClient(..., "", true)`. All capture `log` output the way `TestParseDurationEnv_LogsWarningOnInvalidNonEmptyInput` (`cmd/relay-agent/main_test.go:21`) does - `log.SetOutput(&buf)` with a `defer log.SetOutput(os.Stderr)`.

**a. `TestClient_CreateStreamClient_ClobberWithNoOptionsLineChangesNothingAndWarns`** - fixture is a spec with **no** `Options:` line (the shape `TestClient_CreateStreamClient_Default` already uses at `client_test.go:13-18`). Assert: the written spec has no line matching `optionsLineRe` - **no `Options:` line is synthesised**, which is what proves `setSpecField`'s insert-at-top branch was never reached (spec D2); every other line is unchanged; and the captured log names the client `relay_h_abc`.

**b. `TestClient_CreateStreamClient_ClobberAppendsWhenNeitherTokenIsPresent`** - two required parts in one test:
   - fixture carries `Options:	nocompress unlocked` **and** a `SubmitOptions:	submitunchanged` line. Assert the written token list is exactly `[]string{"nocompress", "unlocked", "clobber"}` - appended at the end, surviving tokens in their original order - **and** that the `SubmitOptions:	submitunchanged` line is byte-identical in the written spec (spec F5).
   - a second subtest whose fixture carries `Options:	clobber nocompress`: assert the written token list is exactly `[]string{"clobber", "nocompress"}`. Idempotence: one `clobber` token, no duplicate, no reorder.

**c. `TestClient_CreateStreamClient_ClobberLeavesAMalformedOptionsLineAlone`** - table with two rows, both asserting the `Options:` line is byte-identical in the written spec and a warning naming the client was logged:
   - `Options:	noallwrite no$1clobber unlocked` - a non-alphabetic token. This is also what keeps a `$`-bearing value out of `regexp.ReplaceAll`'s expansion path in `setSpecField` (spec section 6).
   - `Options:` with an empty value. **This row is not in the spec** - see the plan-time correction. Without the zero-token rule, this input falls through to "append" and produces `Options:\tclobber`, a one-token option set, which is precisely the outcome D2 forbids.

- [ ] **Step 2: Run them and record each RED**

Run: `go test ./internal/agent/source/perforce/... -run "TestClient_CreateStreamClient_ClobberWithNoOptionsLine|TestClient_CreateStreamClient_ClobberAppends|TestClient_CreateStreamClient_ClobberLeavesAMalformed" -count=1 -v -timeout 60s`

Expected REDs, each traced to its own missing guard:
- (a) FAILs on the **log assertion only** (the empty buffer contains no `relay_h_abc`); the "nothing synthesised" half already passes, because Task 2's early return is there. Record that split - it is what tells you the warning, not the behaviour, is what (a) adds.
- (b) FAILs on the token list: actual `["nocompress","unlocked"]`, expected `["nocompress","unlocked","clobber"]`. The idempotence subtest and the `SubmitOptions:` assertion already pass.
- (c) FAILs on both rows: Task 2's implementation rewrites the line through `setSpecField` and logs nothing.

- [ ] **Step 3: Extend `withClobberOption`**

Replace the token loop with the full transform:

```go
	toks := strings.Fields(strings.TrimPrefix(string(line), "Options:"))
	// Zero tokens is malformed, not "append to an empty set": synthesising a
	// one-token Options: line hands p4 an option set whose effect on the
	// unnamed options is unverified, which is the same hazard as inserting the
	// line where none exists.
	if len(toks) == 0 || !allAlphabetic(toks) {
		log.Printf("warning: RELAY_WORKSPACE_CLOBBER is set but client %q has a malformed Options: line %q; leaving the spec unchanged", clientName, string(line))
		return spec
	}
	found := false
	for i, t := range toks {
		// Token equality only. A TrimPrefix(t, "no") sweep would flip
		// noallwrite to allwrite, a different option entirely.
		if t == "noclobber" {
			toks[i] = "clobber"
			found = true
		} else if t == "clobber" {
			found = true
		}
	}
	if !found {
		toks = append(toks, "clobber")
	}
	return setSpecField(spec, "Options", strings.Join(toks, " "))
```

and add the predicate beside it:

```go
func allAlphabetic(toks []string) bool {
	for _, t := range toks {
		for _, r := range t {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return false
			}
		}
	}
	return true
}
```

- [ ] **Step 4: Run the whole package**

Run: `go test ./internal/agent/source/perforce/... -count=1 -v -timeout 180s`
Expected: `ok`, with `--- PASS` on all six `TestClient_CreateStreamClient_*` tests.

- [ ] **Step 5: Prove the anchoring is load-bearing**

Mutation: change `optionsLineRe` to `regexp.MustCompile(`(?m)Options:.*$`)` - drop the `^`.

Run: `go test ./internal/agent/source/perforce/... -run TestClient_CreateStreamClient_ClobberAppends -count=1 -v -timeout 60s`
Expected: RED on the `SubmitOptions:` byte-identity assertion, because `Find` now returns the `SubmitOptions:` line's tail and `setSpecField("Options", ...)` writes the wrong field's value onto the right field's line. Restore by hand (not with `git checkout --`) and re-run as a control; expected `--- PASS`.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/client_test.go
git commit -m "feat: append clobber when absent, and leave a missing or malformed Options: line alone"
```

---

### Task 5: `parseBoolEnv` in the agent

**Files:**
- Test: `cmd/relay-agent/main_test.go` (new `TestParseBoolEnv`)
- Modify: `cmd/relay-agent/main.go`

- [ ] **Step 1: Write the failing test**

Name: `TestParseBoolEnv`. Follow `TestParseDurationEnv_LogsWarningOnInvalidNonEmptyInput`'s log-capture shape exactly (`cmd/relay-agent/main_test.go:21-31`). Signature under test: `parseBoolEnv(name, v string, fallback bool) bool`.

Rows, each asserting both the returned value and whether the log buffer is empty:

| Input | Fallback | Returns | Warns |
|---|---|---|---|
| `""` | `false` | `false` | **no** - empty is not a typo |
| `""` | `true` | `true` | no |
| `"true"`, `"TRUE"`, `"1"`, `"t"`, `"True"` | `false` | `true` | no |
| `"false"`, `"0"`, `"f"`, `"FALSE"` | `true` | `false` | no |
| `"yes"` | `false` | `false` | **yes**, and the warning must contain both `RELAY_WORKSPACE_CLOBBER` and `yes` |
| `"maybe"` | `false` | `false` | yes, naming both |

`"yes"` is in the table deliberately: it is the spelling an operator most plausibly types and the one `strconv.ParseBool` rejects, so a silent fallback there is the failure mode this test exists to make loud.

- [ ] **Step 2: Run it**

Run: `go test ./cmd/relay-agent/... -run TestParseBoolEnv -count=1 -v -timeout 60s`
Expected: build failure - `undefined: parseBoolEnv`. This is the acceptable compile-error RED: the symbol does not exist yet and there is no prior behaviour to get wrong.

- [ ] **Step 3: Implement**

In `main.go`, beside `parseDurationEnv`:

```go
// parseBoolEnv parses a strconv.ParseBool spelling. Empty takes the fallback
// silently; a non-empty unparseable value takes the fallback and warns naming
// the variable. Warn-and-fall-back, not log.Fatalf: the agent is an unattended
// daemon on a render node and every other knob it reads behaves this way. Do
// not copy RELAY_WORKSPACE_MIN_FREE_GB, which discards its parse error and
// turns a typo into a silent zero.
func parseBoolEnv(name, v string, fallback bool) bool {
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Printf("warning: %s=%q is not a boolean; using %v", name, v, fallback)
		return fallback
	}
	return b
}
```

`strconv` and `log` are already imported in `main.go`.

- [ ] **Step 4: Run it**

Run: `go test ./cmd/relay-agent/... -count=1 -v -timeout 60s`
Expected: `--- PASS: TestParseBoolEnv` and every existing test still green.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-agent/main.go cmd/relay-agent/main_test.go
git commit -m "feat: add parseBoolEnv to the agent, warning and falling back on a bad value"
```

---

### Task 6: Carry the flag from `Config` through `Prepare` to the written spec, and wire the env var

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go:89-105` (Config), `:340` (call site)
- Test: `internal/agent/source/perforce/perforce_warm_test.go`
- Modify: `cmd/relay-agent/main.go:73-81`

- [ ] **Step 1: Write the failing test**

Name: `TestProvider_TheClobberConfigReachesTheWrittenSpec`, in `perforce_warm_test.go`.

Build it on the existing scaffolding in that file: `newFakeP4Fixture(t)`, `expectedClientName("h", "//s/x")`, `warmFixtures(fr, client)`, `warmStreamSpec()`, and the registry seeding that `TestProvider_AWarmPrepareStillRewritesTheClientSpec` (`perforce_warm_test.go:313-343`) already performs - read that test and mirror its setup.

`warmFixtures`'s `client -o` fixture almost certainly has no `Options:` line, which would make this test assert the D2 no-op instead of the transform. **Check it, and if so register an explicit `fr.set("client -o -S //s/x "+client, ...)` override carrying an `Options:	noallwrite noclobber nocompress unlocked nomodtime normdir` line** - `fakeRunner.set` writes into a map, so a later `set` on the same key replaces the earlier one.

Two subtests, differing only in `Config{Root: root, Hostname: "h", Client: &Client{r: fr}, Clobber: <true|false>}`:
- `Clobber: true` - the `client -i` call's stdin has an `Options:` token list containing `clobber` and not `noclobber` (compared as a token slice, via a helper of the same shape as `optionsTokensOf`).
- `Clobber: false` - the `client -i` stdin's `Options:` line is byte-identical to the fetched fixture's.

Find the `client -i` call by scanning `fr.calls` for `args` equal to `[]string{"client", "-i"}` rather than by index; `Prepare` makes several calls and the index is not stable.

This is the executed check that replaces the wiring guard the item asked for and that does not exist (spec F6/D3): it pins the `Config`-field-to-spec-bytes hop, which is the rung `idea-2026-08-14` itself says to prefer over an AST parse.

- [ ] **Step 2: Run it**

Run: `go test ./internal/agent/source/perforce/... -run TestProvider_TheClobberConfigReachesTheWrittenSpec -count=1 -v -timeout 60s`
Expected: build failure - `unknown field Clobber in struct literal of type perforce.Config`.

- [ ] **Step 3: Add the field and the pass-through**

In `Config` (`perforce.go:89-105`), after `FreeDiskGB`:

```go
	// Clobber makes every created or repaired client spec carry the p4 option
	// clobber instead of noclobber, so a sync overwrites a writable unopened
	// file rather than aborting. Agent-level and never per-task: the client is
	// shared by every task on the stream, and CreateStreamClient runs on every
	// Prepare. RELAY_WORKSPACE_CLOBBER.
	Clobber bool
```

At `perforce.go:340`, change the trailing `false` to `p.cfg.Clobber`.

- [ ] **Step 4: Run it**

Run: `go test ./internal/agent/source/perforce/... -count=1 -v -timeout 180s`
Expected: `ok`, both subtests `--- PASS`.

- [ ] **Step 5: Wire the env var in `main.go`**

In the `perforce.Config` literal at `cmd/relay-agent/main.go:73-81`, add after `FreeDiskGB`:

```go
			Clobber: parseBoolEnv("RELAY_WORKSPACE_CLOBBER", os.Getenv("RELAY_WORKSPACE_CLOBBER"), false),
```

**This single line is unpinned by any test, and that is a stated cost, not an oversight** (spec D3). It is identical in shape to the `SyncHeartbeatInterval:` and `FreeDiskGB:` lines in the same literal, both of which `docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md:374-378` records as measured-unpinned. Do not paste a fourth copy of `cmd/relay-server`'s AST guard here - that is the exact thing that item exists to stop.

- [ ] **Step 6: Build and run both packages**

Run: `go build ./... && go test ./cmd/relay-agent/... ./internal/agent/source/perforce/... -count=1 -timeout 180s`
Expected: `ok` for both.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_warm_test.go cmd/relay-agent/main.go
git commit -m "feat: wire RELAY_WORKSPACE_CLOBBER through perforce.Config into the client spec"
```

---

### Task 7: README

**Files:**
- Modify: `README.md` - insert one row after line 504 (`RELAY_SYNC_HEARTBEAT_INTERVAL`), the last row of the agent env table.

- [ ] **Step 1: Add the row**

The row must carry five things. Three are the item's acceptance criterion; two are additions the spec argues for (F1, F8):

1. **What it does and its default.** Default `false`. When true, the relay-created client spec's `Options:` carries `clobber`, so `p4 sync` overwrites a writable, unopened file under the workspace root instead of aborting with "Can't clobber writable file".
2. **The shared-workspace exposure, stated in that form.** Relay workspaces are shared across tasks under `ModeShared` admission, so one task's writable output sitting in a synced path can be destroyed by another task's sync. Do **not** write "files you edited by hand" - that understates it.
3. **The opened-file caveat, stated narrowly.** p4 does not clobber a file that is *opened in the client*, so files unshelved into a relay-created pending CL are unaffected. This is a statement about p4's open state, not about importance - do not let a reader hear "your work is safe".
4. **Self-healing (F1).** `CreateStreamClient` runs on every `Prepare`, so setting the variable and restarting the agent rewrites each stream's client spec on its next prepare. No eviction, no manual `p4 client`.
5. **Why this beats a `client_template` (F8).** A template's `-t` is applied only on the cold path, so a prepare that repairs a deleted client drops every template field including `Options:`; this variable is applied after the fetch on every prepare and survives.

Also state the no-op cases so an operator can diagnose an inert knob: if the fetched spec has no `Options:` line, or its value is empty or contains a non-alphabetic token, the spec is left unchanged and the agent logs a warning naming the client.

**Do not name this variable as the remedy for any p4 error** - that is spec F9/D5 and it is what keeps a "turn the protection off" instruction out of a remedy ladder.

- [ ] **Step 2: Check the edit did not corrupt the file**

This repo is CRLF and programmatic document edits have twice shipped defects here.

Run: `git diff --stat README.md`
Expected: `1 file changed, 1 insertion(+)`. **Any larger diffstat means the edit reclassified line endings - stop and revert.**

Run: `git ls-files --eol README.md`
Expected: `i/lf`.

The row must be pure ASCII. Do not use an em dash or any non-ASCII character; if one appears, the file's UTF-8 validity is unverifiable by eye and every check in this repo will still pass.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document RELAY_WORKSPACE_CLOBBER in the agent env table"
```

---

### Task 8: Verify, and OBSERVE the real `Options:` line

**No fixture in this repo shows an `Options:` line produced by a real `p4 client -o -S`** (spec F4). The two that exist are hand-written `Options: clobber`. Two things in this slice are only checkable against the real thing: that the token is spelled exactly `noclobber`, and that p4 accepts a line with a token appended.

- [ ] **Step 1: Run the full unit lane**

Run: `go test ./... -count=1 -timeout 900s`
Expected: `ok` for every package.

- [ ] **Step 2: Vet both tag sets**

Run: `go vet ./...`
Run: `go vet -tags integration ./...`
Expected: no output from either.

- [ ] **Step 3: Instrument the fetched spec temporarily**

In `client.go`, immediately after `spec, err := c.r.Run(ctx, "", args, nil)` and its error check, add:

```go
	log.Printf("OBSERVED CLIENT SPEC:\n%s", spec)
```

This is scaffolding and is removed in Step 5.

- [ ] **Step 4: Run the p4d integration lane and record what p4 emitted**

Run: `go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncAndUnshelve -count=1 -v -timeout 1800s`

Expected: `--- PASS: TestPerforce_E2E_SyncAndUnshelve`, and an `OBSERVED CLIENT SPEC:` block in the captured output.

**A SKIP IS NOT A GREEN.** `startP4dContainer` calls `t.Skip("p4 client binary required on PATH")` when `p4` is missing and skips when the Docker daemon is unreachable, and the package still prints `ok`. Read the `-v` output for the literal `--- PASS:` line. If you see `--- SKIP:`, report **"the p4d lane did not run; the real `Options:` line was not observed"** and say so in the PR body. Do not describe the slice as verified.

Record, verbatim in the commit message for this task, the exact `Options:` line p4 emitted, including its separator character and its full token list, and note the p4d image's p4 version. **Do not translate this observation into a test assertion.** No test in this slice may assert p4's default token set - the design edits whatever tokens are present and asserts nothing about count or order, and hard-coding an observed default would make the suite version-fragile for no gain.

If the observed line disagrees with the assumption in spec F4 - a different spelling than `noclobber`, or a form like `clobber=0` - **stop and report**. The transform's token-equality rule is built on that spelling and would silently no-op.

- [ ] **Step 5: Remove the instrumentation and confirm the tree is clean**

Remove the `log.Printf("OBSERVED CLIENT SPEC:...")` line by hand.

Run: `git status --short`
Expected: empty. If `client.go` still appears, the scaffolding is still there.

- [ ] **Step 6: Re-run the p4d lane clean**

Run: `go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncAndUnshelve -count=1 -v -timeout 1800s`
Expected: `--- PASS`, no `OBSERVED` output. This proves the removal did not break the lane.

- [ ] **Step 7: Race detector**

Run: `make test-race`

On Windows the native lane is unreliable in two distinct ways (a cgo/gcc failure and an intermittent ThreadSanitizer arena allocation failure). The container is the reliable route:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

Expected: all packages `ok`, no data races. This slice adds no concurrency, so this is a regression check. **If the lane is genuinely unavailable, say plainly that `-race` did not run** - do not substitute `-count=N`.

---

## Verification summary

| Command | Timeout | What a non-green means |
|---|---|---|
| `go test ./... -count=1` | 900s | Regression; fix before proceeding |
| `go vet ./...` and `go vet -tags integration ./...` | - | Both tag sets must be clean |
| `go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncAndUnshelve -count=1 -v` | 1800s | Needs Docker **and** `p4` on PATH. **A SKIP is not a green** - report the lane as not run |
| `make test-race` (or the golang:1.26 container) | 600s | Regression check only |

`make test-cli-integration` and `make test-e2e` are **not** in scope: nothing under `internal/cli/` or `web/` changes.

---

## Self-review against the spec

| Spec item | Task |
|---|---|
| R1a mechanical widen | Task 1 |
| R1b `Description:` poison, ordering, token comparison | Task 2 |
| R2 OFF byte-identical, both fixtures | Task 3 |
| R3 idempotence | Task 4 Step 1b, second subtest |
| R4 missing `Options:` line, warn, no synthesis | Task 4 Step 1a |
| R5 neither token + `SubmitOptions:` anchoring + non-alphabetic token | Task 4 Steps 1b, 1c, 5 |
| R6 `TestParseBoolEnv` | Task 5 |
| R7 `Config`-to-spec-bytes | Task 6 |
| R8 README | Task 7 |
| R9 verification incl. the p4d observation | Task 8 |
| D1 token equality, no prefix stripping | Task 2 assertion 2 (`noallwrite` in position 0) and Task 4's `==` comparisons |
| D2 no synthesis on a missing line | Task 4 Step 1a; plus the empty-value case this plan adds |
| D3 no wiring guard; residual seam stated | Task 6 Step 5 |
| D4 warn-and-fall-back, never `Fatalf` | Task 5 |
| D5 `classifyP4Error` deferred | "Do not implement", above; no task |
| D6 OFF is a no-op, not forced `noclobber` | Task 3 |
| D7 no other surface | No proto, jobspec, or API file appears in the file structure table |

## Phase 6 proposals for the conductor

- Amend `docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md` to record that `cmd/relay-agent`'s unguarded `perforce.Config` field count went from two to three (spec D3's own recommendation).
- File a backlog item for the `setSpecField` `$`-expansion hazard: it passes its `value` into `regexp.ReplaceAll`, where `$1` is expanded rather than written literally. Unreachable today across all five call sites, and this slice's token validation keeps it from widening, but it is a latent hazard in a shared helper (spec section 6).
