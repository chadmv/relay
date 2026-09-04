---
date: 2026-09-04
topic: workspace-clobber-option
status: draft
covers:
  - docs/backlog/feature-2026-09-03-workspace-clobber-option.md
---

# An opt-in agent-level `clobber` for relay-managed p4 workspace clients

## 0. How this spec was produced

Gate mode was `autonomous`, so every place the brainstorming flow would put a question to a human,
the call is made here with the reasoning written down and repeated in section 5 so it is cheap to
overturn.

**The tree moved under this backlog item twice on 2026-09-04.** The client-path slice (`c2a7eae`)
made `CreateStreamClient` run on every `Prepare` and moved the whole creation block above head
resolution; the sync-heartbeat slice (`1448ed2`) changed `execRunner.Stream` and added a second
`perforce.Config` field wired from `cmd/relay-agent/main.go`. Every claim below is read off the tree
at `1448ed2`, not off the item.

**F1-F9** are findings from verifying the item. **R1-R9** are red-first steps. **D1-D7** are
decisions.

This is a low-priority item with a small surface. The spec is short on purpose; where it is long, it
is because the item prescribes something that does not exist and the substitute has to be argued.

---

## 1. Verification of the backlog item

### F1 (CONFIRMED, with a corrected premise) - agent-level is right, but not for the reason given

The item argues agent-level because "a client is created once per stream and shared by every task on
that stream". **The first half is no longer true.** Since `c2a7eae`, `perforce.go:340` calls
`CreateStreamClient` unconditionally on **every** `Prepare`, not only on the cold path.

The conclusion survives and gets stronger. A per-task spec field would let two concurrently admitted
tasks flip one shared client's `Options:` against each other on every `Prepare`, one of them possibly
mid-sync. That is the identical hazard `perforce.go:329-335` already names for `client_template`,
which is why the template is applied on the cold path only. An agent-level env var has no such
window: every `Prepare` computes the same value.

**And unconditional creation makes the knob self-healing.** Set the env var, restart the agent, and
the next `Prepare` for each stream rewrites that client's spec. No eviction, no manual `p4 client`.
Worth one README sentence.

### F2 (CONFIRMED) - `CreateStreamClient` as it stands overrides four things, none of them `Options:`

`client.go:130-155`, in order: `setSpecField(spec, "Root", root)`, `setSpecField(spec, "Host", "")`,
`setSpecField(spec, "Owner", "")`, then `removeSpecBlock(spec, "AltRoots")` (added by `c2a7eae`;
pinned by `TestClient_CreateStreamClient_DropsAltRoots`). The result is piped to `p4 client -i`.
`Options:` is never read or written, so p4's default `noclobber` stands. The item's headline is
correct.

### F3 (REFUTED as written) - `setSpecField` cannot edit one token, and it is still the right writer

`setSpecField(spec, field, value)` replaces the **whole line** via `(?m)^Field:.*$` and, when the
field is absent, **inserts `Field:\tvalue` at the top of the spec**. It has no notion of a token.

But it does not need extending. Composition is cheaper: a new reader finds the `Options:` line and
returns its tokens; the caller transforms the token list; the write goes back through `setSpecField`
unchanged. Only the read and the transform are new (D1).

Two consequences that are load-bearing for the tests:

- **`setSpecField` normalizes the separator.** It writes `Options:\t<joined>`, so on the ON path the
  `Options:` line may come back with a tab where p4 emitted something else, and with single spaces
  between tokens. "No other field changes" must therefore be asserted as *every other line
  byte-identical, `Options:` compared as a token list* - not as a whole-spec byte comparison.
- **The insert-if-absent branch must not be reached.** A spec with no `Options:` line must not gain
  one (D2).

### F4 (UNVERIFIED IN-REPO - the implementer must observe it) - what p4's `Options:` line looks like

**Nothing in this repo shows a real `Options:` line produced by `p4 client -o -S`.** The only two
fixtures carrying one (`client_test.go:34` and `:60`) are hand-written `Options: clobber`, authored
for the template and AltRoots tests. `TestClient_CreateStreamClient_Default`'s fixture has **no
`Options:` line at all**, which is why D2 matters: that fixture is live today and must keep passing.

Documented p4 behaviour, stated here as an **assumption to be observed, not as a verified fact**: the
line is `Options:` followed by a tab and six space-separated tokens, each with a bare and a `no`-
prefixed form, defaulting to `noallwrite noclobber nocompress unlocked nomodtime normdir`.

**The design must not depend on that being true.** It edits whatever tokens are present, in place,
and asserts nothing about their count or order. The implementer must nonetheless observe the real
line once, because two things below are only checkable against it: that the token is spelled exactly
`noclobber` (not `no clobber`, not `clobber=0`), and that p4 accepts a line with a token appended
(R5). **The lane exists:** `TestPerforce_E2E_SyncAndUnshelve` drives `Prepare` and therefore
`CreateStreamClient` against a real p4d container (`startP4dContainer`, `p4d_container_test.go`).
Dump the fetched spec there once and record the line in the plan.

### F5 (NEW - not in the item) - `SubmitOptions:` is a real client-spec field and a naive match hits it

A p4 client spec carries `SubmitOptions:` as well as `Options:`; `bug-2026-09-04-a-repaired-p4-client-loses-its-template-fields`
names both. A reader written as `strings.Contains(line, "Options:")` matches `SubmitOptions:` and
would rewrite the wrong field. `setSpecField`'s existing `(?m)^Options:.*$` is anchored and safe;
the new reader must be anchored the same way, and R5 pins it with a fixture that carries both lines.

### F6 (REFUTED - the prescribed remedy names something that does not exist) - there is no wiring guard for `cmd/relay-agent`

The item says to wire the knob "through the same env-to-field path as the other workspace knobs,
covered by the wiring guard ([[idea-2026-08-14-generalize-the-env-to-field-wiring-guard]])".

**That guard does not exist for this binary.** Every guard the cited idea describes -
`TestTrailingLogWindowIsWiredIntoTheHandler`, `TestServerCountersIsWiredByMain`,
`TestWatchdogIsStartedByMain` - lives in `cmd/relay-server`. `cmd/relay-agent`'s only tests are
`TestParseDurationEnv*` and `TestResolveSyncHeartbeatInterval`. The idea item was amended this
morning with exactly this fact: *"`cmd/relay-agent` is a second unguarded package ... deleting the
`SyncHeartbeatInterval:` or `FreeDiskGB:` assignment from the `perforce.Config` literal in `main()`
compiles and leaves every package green."*

So the item's instruction cannot be followed as written. What this slice does instead is D3.

### F7 (REFUTED) - `parseDurationEnv` is not the model, and the agent parses no bool today

`parseDurationEnv` is duration-only (`^(\d+)([smhd])$`). Searching the tree for a bool env parser in
`cmd/relay-agent`: **there is none.** The only `strconv.ParseBool` env sites are
`cmd/relay-server/main.go:160` (`RELAY_ALLOW_AUTO_ENROLL`) and `:190` (`RELAY_ALLOW_SELF_REGISTER`),
both of which `log.Fatalf` on a parse error. The agent's own idioms are the opposite: `parseDurationEnv`
and `resolveSyncHeartbeatInterval` warn and fall back, `RELAY_TELEMETRY_INTERVAL` silently ignores a
bad value, and `RELAY_WORKSPACE_MIN_FREE_GB` **discards `strconv.ParseInt`'s error entirely**
(`main.go:92`), so a typo there is a silent zero.

This is a decision, not an assumption (D4).

### F8 (CONFIRMED, and it makes the env route strictly better than the template route) - a repaired client loses a template's `clobber`

`bug-2026-09-04-a-repaired-p4-client-loses-its-template-fields` is open: `-t` is applied on the cold
path only, so a `Prepare` that repairs a deleted client fetches p4 defaults and drops every template
field, `Options:` included. An operator who set `clobber` inside a `client_template` therefore loses
it on repair. `RELAY_WORKSPACE_CLOBBER` is applied after the fetch on **every** `Prepare`, so it
survives. README should say so, because the template is the workaround an operator reaches for first.

### F9 (REFUTED - defer) - bullet 4's `classifyP4Error` case must not ship in this slice

The item calls it "optional, small". It is neither, for two reasons measured elsewhere:

1. **The clobber stderr is a live misclassification channel, today.**
   `bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr` is OPEN and was amended
   2026-09-03 with a p4d measurement naming this exact case: *"`Can't clobber writable file <local
   path>` (exit 1) puts the LOCAL path in stderr, and the local path is the workspace root joined
   with the caller's own remainder - so a path segment named `disk full` reaches it, and no
   client-path rewrite can remove it."* `classifyP4Error` is a first-match switch over
   `strings.ToLower(classifiableText(err))`, so adding a case here adds an ordering dependency to a
   matcher that is already known to be wrong on this very message.
2. **The remedy would favour the forger.** A caller who can name a path segment
   `can't clobber writable file` could turn an unrelated p4 failure into a diagnosis whose stated fix
   is "set `RELAY_WORKSPACE_CLOBBER=true`" - that is, advertise turning ON fleet-wide silent
   overwriting of local files. This is the shape the Invariants forbid: an option that disables a
   protection must not appear inside a remedy ladder, and the prepare-failure slice already deleted
   one destructive remedy (`RELAY_WORKSPACE_MIN_FREE_GB`) for the same reason.

**Deferred, with a named precondition** (D5): the case may ship once
`bug-2026-09-03-classify-p4-error-...` closes via its option 2 (exit codes or `p4 -ztag`), and only
with a remedy that does not name the knob.

---

## 2. Design

One env var, one `Config` field, one parameter, one new reader, one new transform. No proto change,
no jobspec field, no API surface: a job spec cannot reach this knob, which is the point.

```
RELAY_WORKSPACE_CLOBBER  ->  parseBoolEnv (cmd/relay-agent/main.go)
                         ->  perforce.Config.Clobber
                         ->  Provider.Prepare passes p.cfg.Clobber
                         ->  Client.CreateStreamClient(..., clobber bool)
                         ->  optionsTokens(spec) + token transform + setSpecField
```

**The transform, on the ON path only:**

- Locate the `Options:` line with the same `(?m)^Options:.*$` anchoring `setSpecField` uses (F5).
- Split its value on whitespace. If any token is not `[A-Za-z]+`, leave the spec untouched and warn:
  the line is malformed, and this also keeps a `$`-bearing value out of `regexp.ReplaceAll`'s
  expansion path (section 6).
- If a token equals `noclobber`, replace that token with `clobber`. If a token already equals
  `clobber`, change nothing. Otherwise append `clobber`.
- Write the joined tokens back through `setSpecField`.
- If there is no `Options:` line, leave the spec untouched and warn (D2).

**The OFF path makes no call at all**, so the spec is byte-identical to today's (D6). `log` is
already imported in this package (`sweeper.go`), so the two warnings need no new dependency.

Token equality only, never prefix stripping: `noallwrite`/`allwrite` is a different option and a
`TrimPrefix(tok, "no")` sweep would flip it.

## 3. Sites, why each changes, and what pins it

| Site | Change | Why | Pinned by |
|---|---|---|---|
| `client.go` `CreateStreamClient` | new trailing `clobber bool` param; applies the transform after the `AltRoots` removal | the only place the spec text exists between fetch and `client -i` | R1, R2, R3, R4, R5 |
| `client.go` (new) `optionsTokens` / `withClobberOption` | reader + transform | `setSpecField` is line-granular (F3) | R1, R4, R5 |
| `perforce.go` `Config` | new `Clobber bool` field | matches how `Root` and `SyncHeartbeatInterval` are carried | R7 |
| `perforce.go` `Prepare:340` | passes `p.cfg.Clobber` | the single call site | R7 |
| `cmd/relay-agent/main.go` (new) `parseBoolEnv` | `strconv.ParseBool`, warn-and-fall-back | no bool parser exists in this binary (F7) | R6 |
| `cmd/relay-agent/main.go` `perforce.Config{...}` | `Clobber: parseBoolEnv(...)` | the env-to-field hop | **nothing** - see D3 |
| `README.md` agent env table | one row + the opened-file caveat + F1 and F8 | operators choose this per deployment | R8 |

The `bool` parameter sits after four `string`s, so the same-typed-adjacent-argument transposition
hazard that bit `NewWatchdog` does not apply here; the compiler rejects a swap.

## 4. Red-first sequence

**R1 is first because it carries the field-edit requirement**, exactly as the item asks.

- **R1a (mechanical).** Widen `CreateStreamClient` with `clobber bool` and **ignore it**. Update the
  three existing call sites in `client_test.go` and the one in `perforce.go` with `false`. This step
  exists so that R1b's RED is a failed assertion rather than a compile error; a compile error cannot
  distinguish "not implemented" from "implemented wrong".
- **R1b. `TestClient_CreateStreamClient_ClobberEditsOnlyTheOptionsToken`.** Fixture ordering is
  load-bearing: a `Description:` block containing the word `noclobber` must appear **before** the
  `Options:` line, so that a first-occurrence byte replacement (the fork's implementation) edits the
  Description and leaves `Options:` alone. A poisoned input placed after the real one would let that
  mutant survive. Two assertions, both required: the `Description:` lines are byte-identical to the
  fetched spec, **and** the written `Options:` line's token list contains `clobber` and not
  `noclobber`. Compare **tokens**, never `strings.Contains(spec, "clobber")` - `noclobber` contains
  `clobber`, and a one-sided "Description unchanged" assertion passes a no-op implementation.
- **R2. `TestClient_CreateStreamClient_ClobberOffLeavesTheSpecByteIdentical`.** Two fixtures, both
  with `clobber=false`: one whose `Options:` carries `noclobber`, one whose `Options:` carries
  `clobber` (the template case, which `client_test.go:34` already models). The spec written to
  `client -i` must carry the same `Options:` line as the fetched spec in both. This is the acceptance
  criterion, and the second fixture is what refutes the item's own test sentence (section 7).
- **R3. Idempotence.** `clobber=true` against a spec already carrying `clobber`: one `clobber` token,
  no duplicate, no other token moved.
- **R4. Missing `Options:` line.** `clobber=true` against a fixture with no `Options:` line: no
  `Options:` line is created, the rest of the spec is unchanged, and a warning is logged. Note
  `TestClient_CreateStreamClient_Default`'s existing fixture is exactly this shape and must stay
  green.
- **R5. Neither token, plus `SubmitOptions:`.** `clobber=true` against `Options:\tnocompress unlocked`
  alongside a `SubmitOptions:\tsubmitunchanged` line: `clobber` is appended, the surviving tokens keep
  their order, and `SubmitOptions:` is byte-identical (F5). A second row with a non-alphabetic token
  asserts the leave-untouched-and-warn branch.
- **R6. `TestParseBoolEnv`** in `cmd/relay-agent/main_test.go`: empty returns the fallback with **no**
  warning; `true`/`TRUE`/`1`/`t` return true; `false`/`0` return false; `yes` and `maybe` return the
  fallback **and** log a warning naming `RELAY_WORKSPACE_CLOBBER`. Follow
  `TestParseDurationEnv_LogsWarningOnInvalidNonEmptyInput`'s log-capture shape.
- **R7. `TestProvider_TheClobberConfigReachesTheWrittenSpec`.** Drive `Prepare` through the package's
  existing `fakeP4Fixture` with `Config{..., Clobber: true}` and read `fr.calls[...]`'s `client -i`
  stdin. This is an executed check of the `Config` field to spec-bytes hop, which is the rung the
  wiring-guard item itself says to prefer over a parse. Mirror it with `Clobber: false`.
- **R8. README** row in the agent env table.
- **R9. Verify.** `go test ./...`, `go vet` both tag sets, and the p4d E2E lane once (it is the only
  place a real `Options:` line is observable, F4).

## 5. Decisions

- **D1. A new reader plus `setSpecField` for the write, not an extended `setSpecField`.** Extending
  the line-granular writer with token semantics would change behaviour for `Root`, `Host`, `Owner` and
  `Description`, four call sites that want exactly what it does now. Rejected alternative: a bare
  `strings.Replace(line, "noclobber", "clobber", 1)` scoped to the matched line - correct today, but
  it silently does nothing on a line that carries neither token, which is the F4 case nobody has
  observed yet.
- **D2. A missing `Options:` line is left alone, with a warning; no line is synthesized.** Writing
  `Options:\tclobber` alone would hand p4 a one-token option set whose effect on the five unnamed
  options is unverified (F4), so the safe direction is to change nothing. The cost is that the knob
  can be silently inert; the warning is what makes that observable, and it should name the client.
- **D3. No wiring guard is added for `cmd/relay-agent`, and the residual seam is stated rather than
  papered over** (F6). The three candidates were: build the generalized guard here (that is the whole
  of `idea-2026-08-14`, whose own source comment says do not generalize in a consuming slice); paste a
  fourth copy of the AST guard into a second package (the exact thing that item exists to stop, and it
  would make five copies); or push coverage down to executed checks and accept one unpinned line. This
  spec takes the third. R6 pins env-to-value, R7 pins `Config`-field-to-behaviour, and what remains
  unpinned is the single `Clobber:` line in the `perforce.Config` literal in `main()` - **identical in
  shape to `SyncHeartbeatInterval:` and `FreeDiskGB:`, both measured unpinned in the same literal**.
  Recommendation to the conductor, not done here: amend `idea-2026-08-14` to record that the agent's
  unguarded field count went from two to three.
- **D4. Unparseable means OFF, with a warning; never `log.Fatalf`** (F7). Three reasons. The agent is
  an unattended daemon on a render node, and its every existing env knob warns and falls back; the two
  `Fatalf` bools are server-side security gates parsed with an operator present at boot. Falling back
  to `false` fails toward the non-destructive default. And the warning is what separates this from
  `RELAY_WORKSPACE_MIN_FREE_GB`, whose discarded error makes a typo silent - do not copy that.
  Accepted spellings are `strconv.ParseBool`'s, matching the server's two bools.
- **D5. The `classifyP4Error` case is deferred**, with the precondition in F9.
- **D6. OFF is a no-op, not a forced `noclobber`.** The item's Tests sentence asks for `noclobber` when
  disabled; that would override an operator template that deliberately set `clobber`, and would insert
  an `Options:` line where none exists. Both contradict the item's own first acceptance criterion.
  Section 7 records this as a false criterion.
- **D7. Agent-level scope is enforced by having no other surface.** No proto field, no jobspec field,
  no REST parameter. A job spec cannot request clobber, so the destructive behaviour is a host
  operator's decision only.

## 6. Risks, threat model, and what is explicitly out of scope

- **What the knob does when on:** p4 overwrites any writable, unopened file under the workspace root
  that a sync wants to update. Relay workspaces are **shared across tasks** (`ModeShared` admission),
  so one task's writable output sitting in a synced path can be destroyed by another task's sync. That
  is the deployment's call, and it is why the default is off and why there is no per-job override.
  README must say this in the shared-workspace form, not as "files you edited by hand".
- **The opened-file caveat is narrow and must be stated narrowly.** p4 does not clobber a file that is
  *opened in the client*, so unshelved work in a relay-created pending CL is unaffected. That is a
  statement about p4's open state, not about importance; README should not let a reader hear "your
  work is safe".
- **Not a defence against the wedge, only a way out of it.** With the knob off, the failure the item
  describes is unchanged, and the diagnosis for it stays unclassified (F9). An operator still has to
  read the p4 error.
- **Out of scope, routed to review:** `setSpecField` passes its value into `regexp.ReplaceAll`, where
  `$1` in the value would be expanded rather than written literally. Unreachable today - `Root` is a
  derived path, `Description` is `"relay-task-"+taskID` with a server-assigned UUID, and p4's own
  option tokens are alphabetic - and the D1 token validation keeps this slice from widening it. It is
  a latent hazard in a shared helper and belongs in a backlog item, not here.
- **Out of scope:** the template-repair bug (F8), the classification bug (F9), and generalizing the
  wiring guard (F6). Each is an open item with its own acceptance criteria.

## 7. Audit of the item's acceptance criteria

| Criterion | Verdict |
|---|---|
| "With the option off, behaviour is byte-identical to today." | **True** under D6, and pinned by R2. |
| "With it on, the client spec's `Options:` carries `clobber` and no other field changes." | **True with two required qualifications.** It holds only when the fetched spec has an `Options:` line (D2), and "no other field changes" must be asserted per line, because `setSpecField` re-renders the `Options:` line's own separator (F3). |
| "README documents the option, its default and the opened-file caveat." | **True**, and section 6 adds three things the item does not ask for: the shared-workspace exposure, the self-healing property (F1), and why the env route beats a `client_template` (F8). |
| Tests: "the spec written back contains ... `noclobber` when not [enabled]" | **FALSE against this design, deliberately.** It contradicts the first criterion in the same item, it would override an operator template that set `clobber` (a fixture in `client_test.go` already models one), and it would insert an `Options:` line into a spec that has none. Replaced by R2: when off, nothing is written. |
| Tests: "a template whose `Description:` contains `noclobber` is untouched outside `Options:`" | **True**, and it is R1, the first test, as the item asks. R1b adds the ordering and token-comparison requirements the item does not state, without which the test is vacuous. |
| Proposal 1: "wire it ... covered by the wiring guard" | **FALSE - names something that does not exist** for `cmd/relay-agent` (F6). Substitute in D3. |
| Proposal 4: the `classifyP4Error` case | **Deferred** (F9, D5), on an open bug that measured this exact message as a live misclassification channel. |
