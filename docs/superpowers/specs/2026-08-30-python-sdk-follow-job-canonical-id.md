# A non-canonical job id must not buy a permanently empty stream

- **Date:** 2026-08-30
- **Type:** Server slice with a Python SDK documentation tail. One production change in `internal/api/events.go`; no Python production change; eight prose sites across Go, Python and README that state the old contract as current fact.
- **Closes:** `docs/backlog/bug-2026-08-27-python-sdk-follow-job-hangs-on-noncanonical-job-id.md`
- **Blocked on:** nothing.
- **Phase:** 1 (design). Phase 2 writes the plan.

This spec was produced by a subagent in autonomous gate mode, so every place the brainstorming flow
would ask a human, one of two things happened. Where the evidence in the tree decides the question,
the call is made here with the reasoning written down. Where a genuine fork exists it is a **GATE
QUESTION** in section 1, with a recommendation the conductor may put to the human.

**The filename names the SDK because the item does. The fix does not land in the SDK, and section 5
is the argument for why.** That conclusion is not a preference; it is forced by the measurement in
section 4, which the item asked for and which nobody had taken.

---

## 1. GATE QUESTIONS

**G1. Does the fix land in `internal/api/events.go` (canonicalise `?job_id=` server-side) or in
`python/src/relay/client.py` (canonicalise before subscribing)?** **Recommendation: server-side.**
Argued in section 5. The one-line version: the measured acceptance surfaces (section 4) differ in
**both** directions, and each direction independently kills the SDK-side remedy. Server-accepts /
Python-rejects makes an SDK canonicaliser **incomplete** - there are ids `get_job()` serves that no
Python canonicaliser built on `uuid.UUID` can normalise, so the hang survives for them.
Python-accepts / server-rejects makes it **unsound** - and in three of those rows `uuid.UUID` does
not merely over-accept, it silently produces a *different* id than the string names. A canonicaliser
that runs the server's own parser is the only one that is both complete and sound, and there is
exactly one place that parser lives.

**G2. Does the server also start REJECTING a malformed `?job_id=`?** **Recommendation: no.
Canonicalise only; never reject.** Argued in section 6. Rejecting would be a new 4xx on a documented
contract (`README.md`'s "Validation" paragraph, and an assertion in
`internal/api/events_task_log_integration_test.go` that a `?job_id=not-a-uuid` response is *not* 400).
Canonicalise-only changes the behaviour of **no** currently-working request and fixes every
currently-broken one. That is the whole design.

**G3. Is the Go CLI's `canonicalJobID` deleted once the server canonicalises?** **Recommendation:
no, and this is not defence-in-depth hand-waving.** Argued in section 8. It has **two** readers and
the server fix covers only one of them: `jobSnapshotUnusable` compares `GET /v1/jobs/{id}`'s body id
against the argv spelling, entirely client-side. Deleting it re-opens a *different* bug that the same
2026-08-26 slice closed.

**G4. Does the Python SDK ship any code change at all?** **Recommendation: no code, prose only, and
a version bump is therefore not required.** Argued in section 9, including the honest cost: an SDK
pointed at an *older* `relay-server` still hangs. Section 9 also states why the two obvious SDK-side
mitigations (an eager `get_job` to learn the canonical id; a read timeout) each re-create a hang
rather than removing one.

---

## 2. Contradiction pass on the backlog item

Read once, asking only whether it contradicts itself, contradicts the tree, or prescribes something
that does not exist. **The item is accurate.** Every load-bearing claim in it was checked and holds.
What follows is what was checked, then the two things it gets subtly wrong and the four things it
does not know.

### 2.1 Confirmed, by symbol

- **"`handleEvents` deliberately does not validate or canonicalise `?job_id=`."** True.
  `internal/api/events.go` line 53 is `jobID := r.URL.Query().Get("job_id")`, preceded by a
  four-line comment saying exactly that and calling the asymmetry with `task_id` intentional. The
  `task_id` branch immediately above it does the opposite: `parseUUID`, then a `GetTask` existence
  check, then `logTaskID = uuidStr(taskID)` - **the server already contains the fix, three lines
  earlier, for the other parameter.**
- **"The broker filter is an exact string compare."** True. `internal/events/broker.go`, `Publish`,
  the status branch: `if f.JobID == "" || f.JobID == e.JobID`. No normalisation on either side.
- **"`follow_job(job_id)` passes the caller's string verbatim."** True.
  `python/src/relay/client.py` `follow_job` is `self._require_token(); return
  self._stream_events(job_id)`, and `_stream_events` passes `params={"job_id": job_id}`. Nothing
  touches the string.
- **"The stream sets no read timeout."** True, and deliberately:
  `httpx.Timeout(connect=base.connect, read=None, write=base.write, pool=base.pool)`, with a nine-line
  comment explaining that all four parameters must be explicit. `read=None` is asserted by
  `test_follow_job_yields_events_and_disables_only_the_read_timeout`.
- **"`get_job()` accepts an uppercase or dashless id, because `parseUUID` is `pgtype.UUID.Scan`."**
  True, verified against the actual path rather than assumed: `client.get_job` issues
  `GET /v1/jobs/{job_id}`; `internal/api/server.go`'s mux routes that to `handleGetJob`;
  `handleGetJob` line 637 is `parseUUID(r.PathValue("id"))`; `parseUUID` (`server.go:259`) is a bare
  `pgtype.UUID.Scan`. Section 4 measures what that accepts.
- **"`follow_job` raised `ValueError` on its first frame in every released version, so this hang has
  never been reachable; it becomes reachable with the fix that shipped in the envelope slice."**
  True, and corroborated in the tree twice, independently of the item.
  `docs/backlog/closed/bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys.md`'s Resolution
  section says: "`follow_job()` raised `ValueError` on its first frame in every released version and
  had zero tests. It was observed working against a live server for the first time in the SDK's
  history." And `test_follow_job_yields_events_and_disables_only_the_read_timeout`'s docstring gives
  the mechanism. **So this is a live bug at HEAD, not a latent one**: `python/pyproject.toml` is at
  `0.3.0`, the timeout fix is in the tree, and the first caller to iterate `follow_job` with a pasted
  uppercase id gets the hang.

### 2.2 CORRECTED - the cause of the historical `ValueError` is misattributed

The item says the `ValueError` was fixed by "the envelope slice", which reads as though the
`Event`/envelope decode was the cause. It was not. The cause was `httpx.Timeout(connect=..., read=None)`
raising `ValueError` because httpx takes its four-explicit-parameters branch only when connect, read,
write **and** pool are all set. It was fixed *in* the envelope slice, as a separately-noted repair,
not *by* it. This matters to an implementer who goes looking in `events.py` or `models.Event` for the
history and finds nothing.

Strictly, "on its first frame" is also generous: `_stream_events` is a generator, so the `ValueError`
was raised on the first `next()`, **before the request was sent**. No frame was involved.

### 2.3 CORRECTED - "so a naive SDK-side canonicaliser would make the SDK accept MORE than the server"

Accurate as far as it goes, and it is only one of the two directions. The item frames the risk as
one-sided over-acceptance, and its acceptance criteria follow that framing. **The other direction is
also non-empty, it is larger in kind, and it is the direction that decides the design.** The server
accepts a family of spellings `uuid.UUID` refuses outright (section 4.3), so an SDK canonicaliser is
not merely risky - it is *incapable of closing the bug* for those ids. The item's own "Done When"
list cannot be satisfied by any SDK-side remedy, which is a thing the item does not know about
itself.

### 2.4 Four things the item does not know

1. **`pgtype.UUID.Scan` never examines the separator bytes.** Section 4.2. This is the fact that
   makes the server's surface strictly larger than Python's on the 36-byte form.
2. **There is no third consumer.** Checked, section 3. The item does not claim one, but the
   conductor asked, and "a defect class with three consumers is a different item" - it has two.
3. **Every production publisher already emits a canonical `JobID`.** Section 7.1. Eight
   `broker.Publish` sites carry a `JobID` and all eight build it with `uuidStr(...)` from a
   `pgtype.UUID` read out of the database. That is what makes canonicalising the *subscribe* side a
   complete fix rather than half of one.
4. **The un-canonicalised contract is written down in eight places.** Section 10. The repo's dominant
   defect is wrong prose about correct code, and this change invalidates a comment in the file it
   edits, a comment in a file it does not edit, two test comments, and a README paragraph.

---

## 3. The consumer census

The conductor asked whether the SPA, the MCP server, or the Go CLI subscribe to `/v1/events` with a
caller-supplied `job_id`. Measured by grep over each tree, and the answer changes the item's weight:
**two consumers, not three.**

| Consumer | Subscribes to `/v1/events`? | With `job_id`? | Canonicalises? |
|---|---|---|---|
| Go CLI, `internal/cli/logs.go` | yes, `jobEventsPath` | **yes**, from argv | **yes**, `canonicalJobID`, since 2026-08-26 |
| Python SDK, `python/src/relay/client.py` | yes, `_stream_events` | **yes**, verbatim | **no** - this bug |
| SPA, `web/src/` | yes | **no** - `?task_id=` only | n/a |
| MCP server, `internal/mcp/` | **no** - no `/v1/events` reference at all | n/a | n/a |

The SPA's abstention is deliberate and documented: `web/src/jobs/api.ts` carries the comment "Only
?task_id= is sent. Adding ?job_id= would put status frames on the same [connection]". A grep for
`job_id=` across `web/src` returns only `scheduled_job_id` on a REST list route. So the SPA is not
exposed - **and it is exposed the moment somebody adds `job_id` to that stream**, which is a live
possibility given the comment describes it as a considered option. That is an argument for the
server-side fix that the item cannot make, because the item is scoped to a client.

`internal/relayclient.StreamEvents` is the shared Go transport both the CLI and the worker
integration tests use; it takes a fully-built path string and does no id handling, so it is not a
consumer in this sense.

---

## 4. The measured acceptance surfaces

This is the section the item asked for and the reason this slice is not a port.

### 4.0 How it was measured, and the one honest caveat

**Bash is disabled in this session, so nothing here was executed.** Both parsers were measured by
reading their exact source at the exact versions this repo pins. That is a stronger instrument than
it sounds for these two functions - each is under twenty lines with no branching on anything but
string length - but it is not the same as running them, and the distinction is marked per row.

- **Server:** `github.com/jackc/pgx/v5 v5.9.1` (pinned in `go.mod`),
  `pgtype/uuid.go` lines 35-53 (`parseUUID`) and 73-90 (`Scan`), plus Go's `encoding/hex`
  `reverseHexTable` and `Decode` (`C:\Program Files\Go\src\encoding\hex\hex.go` lines 18-34, 87-113).
- **Python:** CPython **3.13.13** (the interpreter behind `python/.venv`, per `pyvenv.cfg`),
  `Lib/uuid.py` `UUID.__init__` lines 177-182.

Rows derived purely from the string-slicing and character-table halves are marked **[source-exact]**:
they follow from the code with no interpretation. Rows that additionally depend on the grammar
CPython's `int(s, 16)` accepts are marked **[unexecuted]** and **must be confirmed by running them
before any test hard-codes an expected value.** That confirmation is an acceptance criterion
(section 12).

### 4.1 The server's surface, stated exactly

```go
switch len(src) {
case 36:
    src = src[0:8] + src[9:13] + src[14:18] + src[19:23] + src[24:]
case 32:
    // dashes already stripped, assume valid
default:
    return dst, fmt.Errorf("cannot parse UUID %v", src)
}
buf, err := hex.DecodeString(src)
```

`pgtype.UUID.Scan(s)` succeeds **if and only if**:

- `len(s) == 32` **bytes** and all 32 bytes are in `[0-9a-fA-F]`; or
- `len(s) == 36` **bytes** and the 32 bytes at indexes `0-7, 9-12, 14-17, 19-22, 24-35` are all in
  `[0-9a-fA-F]`.

Nothing else. No prefix handling, no brace handling, no whitespace tolerance, no sign, and the length
test is over **bytes**, so a multi-byte character cannot occupy a hex position (its continuation byte
exceeds `0x7f`, which `reverseHexTable` maps to `0xff`).

### 4.2 The load-bearing detail nobody had noticed

**On the 36-byte branch, the bytes at indexes 8, 13, 18 and 23 are sliced out and never examined.**
They do not have to be hyphens. They can be any single byte. `hex.DecodeString` never sees them, so
`7e660488_1234_4321_8888_abcdefabcdef` and `7e660488:1234:4321:8888:abcdefabcdef` are, to this
server, the same job as the canonical spelling - and `GET /v1/jobs/<either>` answers 200 today.

Go's hex table was read rather than assumed, because the case-insensitivity claim rests on it:
`0x30-0x39` map to 0-9, `0x41-0x46` **and** `0x61-0x66` both map to 10-15, everything else maps to
`0xff` and is rejected. So uppercase is accepted, and it is accepted by a table lookup, not by a
normalisation step.

### 4.3 Server accepts, Python rejects - the set that makes an SDK fix incomplete

Base id throughout: `7e660488-1234-4321-8888-abcdefabcdef`.

| Exact string | `pgtype.UUID.Scan` | `uuid.UUID()` | Why |
|---|---|---|---|
| `7e660488_1234_4321_8888_abcdefabcdef` | **accepts** | rejects | 36 bytes; separators sliced unexamined. Python: no `-` to remove, so `len == 36 != 32` -> `ValueError`. **[source-exact]** |
| `7e660488:1234:4321:8888:abcdefabcdef` | **accepts** | rejects | identical mechanism. **[source-exact]** |
| `7e660488 1234 4321 8888 abcdefabcdef` | **accepts** | rejects | identical mechanism; spaces are just bytes at separator positions. **[source-exact]** |
| `7e660488-1234*4321-8888-abcdefabcdef` | **accepts** | rejects | **mixed** separators. Python removes the three hyphens, leaving 33 chars. **[source-exact]** |

This set is non-empty, and it is non-empty for a *structural* reason (four unexamined byte positions),
not a corner case. **No Python canonicaliser built on `uuid.UUID` can normalise any row of it.** A
canonicaliser modelled on `canonicalJobID`, which passes an unparseable string through unchanged,
leaves every one of these subscribing to nothing - the exact bug, unfixed, for ids `get_job()` serves.

### 4.4 Python accepts, server rejects - the over-acceptance the item warned about

| Exact string | `uuid.UUID()` | `pgtype.UUID.Scan` | Why |
|---|---|---|---|
| `{7e660488-1234-4321-8888-abcdefabcdef}` | **accepts**, canonical value | rejects | `.strip('{}')`. 38 bytes -> `default` branch. **[source-exact]** |
| `urn:uuid:7e660488-1234-4321-8888-abcdefabcdef` | **accepts**, canonical value | rejects | `.replace('urn:','').replace('uuid:','')`. 45 bytes. **[source-exact]** |
| `7e660488-1234-4321-8888-abcdefabcdef-` | **accepts**, canonical value | rejects | `.replace('-','')` removes hyphens anywhere. 37 bytes. **[source-exact]** |
| `7e6604881234432188-88abcdefabcdef` | **accepts**, canonical value | rejects | hyphen at a non-canonical position. 33 bytes. **[source-exact]** |
| `+7e660488123443218888abcdefabcde` | **accepts**, and the value is **not** the id the caller wrote - it is `07e66048-8123-4432-1888-8abcdefabcde` | rejects | `int('+<31 hex>', 16)` takes the sign; the result zero-pads to 128 bits. `+` is `0xff` in the hex table. **[unexecuted]** - the padded literal is computed by hand and must be confirmed, though the *property* (a value that is not the string) follows from the sign grammar |
| `0x7e660488123443218888abcdefabcd` | **accepts**, value `007e6604-8812-3443-2188-88abcdefabcd` | rejects | `int('0x<30 hex>', 16)` takes the base prefix. **[unexecuted]** |
| a 32-char string with one internal `_` between hex digits | **accepts**, value shifted | rejects | PEP 515 digit separators are legal in `int(s, 16)`. **[unexecuted]** |

The last three rows are the ones that matter and they are worse than "over-accepts". An SDK
canonicaliser on `uuid.UUID` would take `+7e660488123443218888abcdefabcde` and **subscribe to a
different job**, silently, having been handed a string the server would have refused. That is a
downgrade from a hang to a wrong answer.

### 4.5 Agreement set

| Exact string | Both |
|---|---|
| `7e660488-1234-4321-8888-abcdefabcdef` | canonical; the only form the server ever *emits* |
| `7E660488-1234-4321-8888-ABCDEFABCDEF` | uppercase |
| `7e660488123443218888abcdefabcdef` | dashless |
| `7E660488123443218888ABCDEFABCDEF` | dashless uppercase |

The first three are exactly `internal/cli/logs_test.go`'s `canonicalSpelling`, `uppercaseSpelling`
and `dashlessSpelling`. Reusing those literals in the new tests is deliberate: they are already the
repo's statement of "spellings a real operator pastes".

### 4.6 The conclusion the table forces

Both differences are non-empty, and each kills a different half of the SDK remedy:

- **B \ A non-empty** (section 4.3) => an SDK canonicaliser is **incomplete**. It cannot satisfy the
  item's first acceptance criterion for those ids.
- **A \ B non-empty** (section 4.4) => an SDK canonicaliser is **unsound**. It violates the item's
  second acceptance criterion by construction, and in three rows does so by inventing an id.

A canonicaliser that runs the *server's own parser* has neither problem, because "what the server
accepts" is then not a model of the server - it is the server. There is exactly one place that
parser already lives, and `handleEvents` already calls it eleven lines above the bug.

---

## 5. The design

Three lines in `internal/api/events.go`, replacing the pass-through at line 53.

```go
// ?job_id= is still NOT validated: an unknown or malformed job id yields an
// open, permanently empty stream, which is an existing contract with existing
// clients. What it IS, now, is canonicalised - see below.
jobID := r.URL.Query().Get("job_id")
if u, err := parseUUID(jobID); err == nil {
    jobID = uuidStr(u)
}
```

That is the entire behaviour change. It is the same two-step the `task_id` branch performs at line 47
(`logTaskID = uuidStr(taskID)`) with the existence check omitted, because there is no existence check
to preserve here and G2 declines to add one.

**The `err == nil` guard is the whole correctness argument and must not be read as noise.**
`parseUUID` returns `pgtype.UUID{}` on failure - `Valid: false` - and `uuidStr` returns the **empty
string** for an invalid UUID. So an implementation that renders unconditionally
(`u, _ := parseUUID(jobID); jobID = uuidStr(u)`) turns every unparseable `?job_id=` into `""`, and
`Filter{JobID: ""}` is the broker's **broadcast** subscription: `Publish`'s status branch delivers to
it whenever `f.JobID == ""`. A typo would silently promote a single-job subscription into a
whole-cluster status feed. That is a fail-**open** mutation of a fail-closed design, it is one
deleted character away, and section 11.4's M2 exists for it and nothing else. Gate the side effect
on the parse having actually succeeded - the same shape as gating a write on a fence having actually
matched.

**Why this is complete rather than merely broad.** The filter is an exact string compare against
`Event.JobID`, and every production publisher builds that field the same way. Measured by reading
every `broker.Publish` call site in non-test Go: **eleven** sites, of which **eight** carry a
`JobID`, and all eight are `JobID: uuidStr(...)` over a `pgtype.UUID` read from the database -
`internal/api/jobs.go` twice (cancel, retry), `internal/worker/handler.go` three times (task status,
job terminal, task log), `internal/scheduler/dispatch.go` three times. The other three are `worker`
events, which carry no job scope by design. So the publish side emits exactly one spelling, and
canonicalising the subscribe side onto that spelling closes the gap over the **whole** server
acceptance surface, including section 4.3's rows that no client could ever have handled.

**Why the parser cannot drift from itself.** `canonicalJobID`'s comment makes the drift argument for
the CLI and is careful to scope it: "Only the PARSE half is shared ... The RENDER below is a
hand-written duplicate: the format string is the sixth production copy of it." Inside
`internal/api`, both halves are shared - `parseUUID` and `uuidStr` are the same unexported functions
`handleGetJob` and `handleEvents`'s `task_id` branch already use. This slice adds a *zeroth* copy of
nothing.

### 5.1 What was considered and rejected

**Canonicalise in the SDK.** Rejected on section 4.6: incomplete in one direction, unsound in the
other. Any repair - hand-rolling `pgtype`'s grammar in Python so the surfaces match - is a
reimplementation of the server's acceptance rules in a second language with no shared test, which is
the drift the item is worried about, relocated rather than removed. It would also have to be
maintained against a pgx upgrade that nobody would think to check.

**Reject in the SDK what the server would reject.** Same problem, plus a worse failure mode: the SDK
would have to *encode the server's grammar* to decide what to refuse, and a grammar copy that is too
strict turns a working `follow_job` into an exception. Section 4.3's rows are precisely where a
plausible-looking Python guard gets it wrong, and they are the rows a reviewer would never think to
try.

**Validate and 400 in the server.** G2. Section 6.

**Have `follow_job` learn the canonical id from `get_job` first.** Rejected, and this is the option
that looks free. `canonicalJobID`'s own comment names the cost: "reading one first to learn the id
reopens the terminal-before-subscribe race the snapshot exists to close." The shape here is worse
than for the CLI, not better: `follow_job`'s documented contract is that the caller breaks on a
terminal `job` frame. A job that reaches terminal in the window between the `get_job` and the
`stream` publishes its terminal frame to nobody, and the caller - following the README's own example
loop - blocks forever. That is the same hang this slice exists to remove, re-created by its own fix,
which is the repo's recorded backstop-recreates-the-defect shape.

**Normalise inside the broker instead of the handler.** Rejected. `internal/events` deliberately
knows nothing about UUIDs - its `Filter` is two opaque strings, and `TaskID` routing depends on that
opacity. Teaching it `pgtype` would give the events package a `pgx` dependency and would put id
policy in the fan-out path (per publish, per subscriber) instead of once per connection.

---

## 6. G2: canonicalise, never reject

The tempting extra step is to answer 400 on a `?job_id=` that does not parse, matching `task_id`.
Declined, for three reasons in increasing order of weight.

1. **It is a documented contract.** `README.md`'s "Validation" paragraph says `?job_id=` is not
   validated, that an unknown job yields an open but permanently empty stream, and that "this
   asymmetry is deliberate". `internal/api/events.go`'s own comment repeats it. Changing that is a
   separate decision with a separate blast radius, and it is not what the item asks for.
2. **A test asserts the current behaviour and would go RED.**
   `internal/api/events_task_log_integration_test.go`, `TestEvents_TaskIDValidation`, lines 89-95:
   it issues `GET /v1/events?job_id=not-a-uuid` and asserts `assert.NotEqual(t,
   http.StatusBadRequest, rec.Code)`. Canonicalise-only keeps it green, because `parseUUID` fails and
   the string passes through untouched. **This is the check that proves the design is additive**, and
   the plan must confirm it stays green rather than editing it.
3. **Rejection fixes nothing this slice is for.** Every id in section 4.3 and section 4.5 *parses*.
   The bug is entirely inside the accepted set. A 400 would only change what happens to strings that
   are already, correctly, streaming nothing.

**The one thing canonicalise-only does not fix, stated so nobody thinks it does.** A well-formed id
for a job that does not exist still yields an open, permanently empty stream, indistinguishable from
a job that is simply quiet. That is unchanged, deliberate, documented, and out of scope. Section 13
proposes an item for the general shape.

---

## 7. Load, failure modes, threat model, invariants

**Load.** One `pgtype.UUID.Scan` and one `fmt.Sprintf` **per connection**, not per event, in a
handler that then holds a long-lived stream. Both operate on a bounded 32-or-36-byte input. Against
the `Subscribe` call on the next line - a mutex acquire and two map writes - this is unmeasurable.
Nothing moves into the `Publish` fan-out path, which is the only hot path in this subsystem.

**Failure mode before.** An open HTTP connection, a live broker subscription holding a 64-slot
channel, and zero frames, forever - on a client with `read=None` and, in the Go CLI's case before
2026-08-26, no deadline either. The server has no heartbeat, so nothing distinguishes it from a
healthy idle stream at any layer. A caller loop consuming `follow_job` never returns and never
raises.

**Failure mode after.** For any spelling the server accepts, the frames arrive. For anything else,
byte-identical behaviour to today.

**The failure mode the IMPLEMENTATION can introduce.** Section 5's `err == nil` guard. An
unconditional render maps every unparseable id to `""`, which is the broker's broadcast filter, so a
malformed `?job_id=` would receive **every job's** status frames instead of none. It is not an
authorization escalation - `GET /v1/events` with no parameters is already an authenticated
whole-cluster status feed, so the same token could have asked for it directly - but it is a silent
change of scope from what the caller wrote, and it is the one way this three-line change can be worse
than doing nothing.

**Threat model.** `?job_id=` is attacker-controlled in the sense that it is caller-controlled, and
this change does not widen what a token can reach. Three specific checks:

- **No authorization consequence.** `GET /v1/events` has no per-job ownership gate at all - the
  handler's own comment says so, and `handleGetJob` likewise serves any job to any authenticated
  token. Canonicalising maps several spellings of *the same job* onto one; it cannot map a caller
  onto a job they could not already have named canonically with the same token. There is no
  collision to exploit: `uuidStr` is injective on the 16 bytes, and two inputs canonicalise together
  only when they decode to the same 16 bytes.
- **No new echo.** The value goes into a `Filter` struct and is compared. It is never written into
  the response, never logged, never interpolated into SQL or a URL. Canonicalising it narrows the set
  of bytes that can reach the broker map, which is weakly good and is not claimed as a security
  benefit.
- **Pre-existing and untouched:** the pass-through branch still admits an unbounded-length
  caller-supplied string as a map key held for the connection's lifetime. Bounded in practice by
  connection count and `ratelimit.go`, not by anything in this handler. Not introduced here, not
  fixed here, proposed in section 13.

**Invariants.** No backend invariant is touched: no write to `tasks.status` or `task_logs`, no
epoch, no stream sender, no teardown. The invariant this slice honours by analogy is the one behind
**single job-spec pipeline** and **single JSON entry point** - one canonical form, decided in one
place, rather than each consumer modelling the server's grammar for itself. The SDK-side remedy is
the version of this change that violates that principle, and section 5.1 is that argument in full.
The `err == nil` guard is the local instance of **gate the side effect on the check having actually
matched**.

---

## 8. G3: `canonicalJobID` stays

`internal/cli/logs.go`'s `canonicalJobID` becomes redundant for the subscription once the server
canonicalises. It is not redundant, and the reason is written in its own comment: **it has two
readers.**

> "Two things here read the id and NEITHER tolerates a second spelling. `jobSnapshotUnusable`
> compares the body's id against ours, so a canonical answer to a non-canonical request reads as a
> response about a different job. And `handleEvents` deliberately does not validate or canonicalise
> `?job_id=` ..."

The server fix covers the second reader only. The first is a pure client-side comparison between
argv and the id in `GET /v1/jobs/{id}`'s body, and `relay logs 7E660488-...` would resume reporting
"the response is about a different job" the moment `canonicalJobID` is removed. That is a distinct
bug the same 2026-08-26 slice closed, and it is not in scope here.

What *does* change is its **justification**: two of its four paragraphs assert that `handleEvents`
does not canonicalise. Correcting that comment without deleting the function is required scope
(section 10), and it is the highest-risk prose edit in this slice, because the obvious edit - delete
the paragraph - deletes the sentence explaining why argv is canonicalised *before either request
line is built* rather than after the snapshot.

---

## 9. G4: the Python SDK ships prose, and the cost of that

No production Python change. `follow_job` is correct against a fixed server, and section 4.6 says any
Python-side canonicaliser is worse than none.

**The cost, stated rather than buried: an SDK from this tree pointed at an OLDER `relay-server` still
hangs on an uppercase id.** That is real and the spec should not pretend otherwise. Three things
bound it:

1. The SDK and the server ship from one repository and `python/README.md` makes no version-skew
   guarantee in either direction.
2. The item's own history means there is no installed base to protect: `follow_job` raised on the
   first `next()` in every released version before `0.2.0`, so no deployed caller is relying on
   today's behaviour.
3. No SDK-side mitigation is available that does not create a worse failure. Canonicalising is
   unsound (4.4). Rejecting requires a grammar copy (5.1). An eager `get_job` re-creates the hang via
   the terminal-before-subscribe race (5.1). A read timeout would convert a healthy idle stream into
   a spurious error, and `_stream_events`'s nine-line comment exists specifically to keep `read=None`.

So the SDK's contribution is **advertisement at the point the failure is read** - `follow_job`'s
docstring and `python/README.md`'s "Following a job" section gain a sentence stating that the server
normalises the id spelling, and that against a server older than this change a non-canonical id
yields a permanently empty stream. Per the project's rule that a signal discloses its properties
where it is read, that sentence is required scope, not documentation polish.

**No version bump.** `python/src/relay/_version.py` and `pyproject.toml` are kept in lockstep by
`test_version_files_are_in_lockstep`, so moving one alone is RED. A docstring change moves neither.
The plan may argue for a patch bump to carry the README text to PyPI; that is a packaging decision,
not a correctness one.

---

## 10. The prose sweep: eight sites that become false

The repo's dominant defect is wrong prose about correct code, eight iterations running. This change
makes a specific, checkable sentence false in eight places. Named by symbol so the plan does not have
to rediscover them.

**Production, Go:**

1. `internal/api/events.go`, the comment above `jobID :=` - "?job_id= is deliberately NOT validated".
   Must become: not validated, *and now canonicalised*, with the asymmetry restated as being about
   **rejection**, not about normalisation. This is the sentence the whole slice turns on.
2. `internal/cli/logs.go`, `canonicalJobID`'s doc comment - "handleEvents deliberately does not
   validate or canonicalise `?job_id=` ... so a non-canonical subscription matches nothing that is
   ever published". False after this slice. See section 8 for what must survive the edit.

**Tests (the comments, not the assertions):**

3. `internal/api/events_task_log_integration_test.go`, in `TestEvents_TaskIDValidation` - "?job_id=
   validation is deliberately UNCHANGED". Still true about *validation*; the comment must say so
   precisely now that canonicalisation is not.
4. `internal/cli/logs_test.go`, `fakeUUIDSpellingServer`'s doc comment - "the one thing it does not
   do" is exactly the thing the server now does. The fake models the OLD server. **Decide
   explicitly** whether the fake is updated: it exists to prove the CLI works against a server that
   does not canonicalise, which is still a server the CLI may meet, so the recommendation is to keep
   the fake's behaviour and correct the comment to say it models the pre-2026-08-30 server.
5. `internal/cli/logs_test.go`, the block comment beginning "A job id is argv, and an operator pastes
   whatever their source gave them" - unaffected in substance, checked, listed so the sweep is
   complete rather than sampled.

**README:**

6. `README.md`, the "Validation" paragraph under Events - "`?job_id=` is not validated - an unknown
   job yields an open but permanently empty stream. This asymmetry is deliberate". Still true, and
   now incomplete: it must add that accepted spellings are normalised to the canonical form, so a
   dashless or uppercase id subscribes to the job it names.

**Python:**

7. `python/src/relay/client.py`, `follow_job`'s docstring - section 9.
8. `python/README.md`, "Following a job" - section 9.

A grep is the *instrument* for finding these, not the claim. The patterns that hit them at HEAD are
`deliberately NOT validated`, `does not validate or canonicalise`, `is not validated`, and
`canonicalJobID`. A reader must confirm the replacements are true; the grep can only show the
known-wrong sentences are gone.

---

## 11. RED-first test design

Two layers, deliberately, because the project's rule is that asserting a helper proves nothing about
the code consuming it, and asserting only the wiring leaves the grammar unpinned.

### 11.1 The headline test

**`TestEvents_JobIDSpellingIsCanonicalisedNotRejected`**, in
`internal/api/events_task_log_integration_test.go` (build tag `integration`, `package api_test`,
`make test-integration`). It goes in that file rather than a new one because
`newTestServerWithBroker` and `gateWriter` already live there and are exactly the tools needed.

Shape, per spelling in `{underscore-separated, uppercase, dashless}`:

1. Seed a real job via `seedTaskViaAPI`, giving the canonical `jobID` the server emits.
2. Derive the non-canonical spelling **from that id by explicit transformation in the test**
   (`strings.ReplaceAll(id, "-", "_")`, `strings.ToUpper`, `strings.ReplaceAll(id, "-", "")`) - never
   by calling any production canonicaliser. A fixture built out of the code under test cannot detect
   drift in it, which is the argument `fakeUUIDSpellingServer`'s comment already makes.
3. `GET /v1/events?job_id=<non-canonical>` on a `gateWriter`, in a goroutine, and wait on
   `gw.flushed() >= 1` - the file's existing deterministic barrier for "the subscription is live".
   No sleeps.
4. `broker.Publish(events.Event{Type: "job", JobID: <canonical>, Data: ...})` with a discriminating
   payload.
5. Assert the frame arrives, within `require.Eventually`.

**At HEAD it is RED, and red for the reason the fix addresses**: the filter string is the
non-canonical spelling, the published `JobID` is canonical, `f.JobID == e.JobID` is false, nothing is
delivered, and `Eventually` times out. **Reverting the three-line change restores exactly that
failure.** Nothing else in the slice can make it pass.

**The bounding requirement.** `TestEvents_TaskIDValidation`'s comment states it: a httptest request's
context is never cancelled, so a handler that streams forever hangs the package rather than failing
the test. Every probe here must carry its own `context.WithTimeout` or an explicit cancel, and the
assertion must be a bounded `Eventually`, not a blocking channel receive.

**The underscore case is not decoration and it is FIRST for a reason.** It is the section 4.3 row,
and it is the single assertion that discriminates the server-side fix from every SDK-side one - a
slice that fixed this in Python would fail it. A discriminating input placed last cannot detect an
early-exit defect, so it leads and the two ordinary spellings follow.

### 11.2 The both-directions pin the item demands

**`TestEvents_JobIDRejectedSpellingsAreNotCanonicalised`**, same file. For each of
`{7e660488-1234-4321-8888-abcdefabcdef}` (braced), `urn:uuid:7e660488-...`, and
`+7e660488123443218888abcdefabcde`:

- The response is **not** 400 (the contract in section 6 holds).
- A `job` frame published with the canonical id **does not arrive**, within a bounded wait.

This is the item's second acceptance criterion made executable: a spelling the server rejects must
not be silently turned into one it accepts. It is RED against any implementation that reaches for
Python's `uuid.UUID` semantics, and it is the guard against a future "improvement" that adds brace or
`urn:` handling to the server to be helpful. **This test is the reason the acceptance-surface table
belongs in the repo and not only in this spec** - it encodes the difference as an assertion.

It is also the test that kills the unconditional-render mutation of section 5, because a rejected
spelling turned into `""` becomes a broadcast subscriber and *does* receive the published frame.

`mustNotReceive`-style negative assertions must be preceded by a positive control in the same test,
per the file's existing convention ("Positive expectations first, so the negatives below cannot pass
vacuously"): publish one canonical-subscription frame that *is* received before asserting the
non-canonical ones are not.

### 11.3 The grammar test, default lane

**`TestCanonicalJobIDSpellings`**, a new `internal/api/events_test.go`, **`package api`, no build
tag** - the default lane, no Docker. `internal/api/server_counters_test.go` establishes that a
`package api` unqualified test file is already how this package does in-package unit tests, so this
introduces no new pattern.

It table-tests the parse-and-render pair over section 4's rows: the four agreement spellings and the
four section-4.3 spellings canonicalise to the identical output; the seven section-4.4 spellings pass
through **unchanged**. It is the cheap, exhaustive statement of the surface, it runs on `make test`
with no container, and it is the place a pgx upgrade that narrowed or widened `Scan` would be caught.

It is **not** sufficient on its own and must not be written as though it were: it says nothing about
whether `handleEvents` calls the thing. 11.1 is the wiring proof. Both, or neither is worth much.

### 11.4 Mutation kill table

Each row needs a non-empty RED set, and the control must die.

| # | Mutation | Expected RED |
|---|---|---|
| M1 | delete the canonicalisation from `handleEvents` entirely | 11.1, all three spellings. **Not** 11.3 - which is the point: the grammar test alone cannot see this |
| M2 | drop the `err == nil` guard: `u, _ := parseUUID(jobID); jobID = uuidStr(u)` | 11.2, all three rows. A rejected spelling becomes `""`, which is the broadcast filter, so the frame the test asserts must NOT arrive does arrive. Section 5 and section 7 |
| M3 | add a 400 on parse failure, matching the `task_id` branch | `TestEvents_TaskIDValidation`'s `assert.NotEqual(t, http.StatusBadRequest, ...)`, and 11.2 |
| M4 | hand-write the render with one group width wrong (e.g. `%08x-%04x-%04x-%04x-%011x`) | 11.3, all rows. This is the mutation 11.3 exists for |
| M5 (control) | `Publish`'s `f.JobID == e.JobID` -> `f.JobID != e.JobID` | `internal/events/broker_test.go` must die. If it survives, the harness did not apply the mutation |

M5 is a control, not a coverage claim. Four mutations in a row have silently failed to apply on this
repo under CRLF and reported "survived", so one that **must** die runs first. And per the CRLF note
in CLAUDE.md: after any programmatic mutation, check the diffstat and `git ls-files --eol` before
concluding anything from the result.

### 11.5 What is NOT tested, and why

**No new Python test.** There is no Python production change to pin, and
`python/tests/integration/` is a manual lane not in CI, so an integration assertion there would be a
claim nobody runs. If the plan wants one anyway, the honest form is a note on the existing smoke test
rather than a new gate.

**No Go CLI test change.** `fakeUUIDSpellingServer` models a server that does not canonicalise, and
after this slice that is a *historical* server rather than the current one - which is still a server
`relay logs` may be pointed at. Its assertions stay; its comment is corrected (section 10, site 4).

---

## 12. Acceptance criteria

Carried from the item, corrected where its wording is false against this design. A criterion that is
false against its own design produces a test that fails on correct code.

Carried unchanged:

- **A non-canonical but server-acceptable job id yields the same frames as its canonical spelling.**
  Satisfied for the *whole* server acceptance surface, including section 4.3's rows, which is more
  than the item asked for and is the difference between this design and every SDK-side one.
- **The acceptance surface is pinned in BOTH directions.** Test 11.2.
- **A test covers uppercase, dashless, and at least one spelling `uuid.UUID` takes and the server does
  not.** Tests 11.1 and 11.2. The braced and `urn:uuid:` rows are the "`uuid.UUID` takes and the
  server does not" cases.

**Restated** (the item's framing assumes the fix is in the SDK):

- ~~"a naive SDK-side canonicaliser would make the SDK accept MORE than the server"~~ ->
  **No canonicalisation is added to the SDK at all, in either direction, and section 4.3 is the
  reason: an SDK canonicaliser would also accept LESS than the server, which the item does not
  consider and which no Python-side remedy can repair.**

Added by this spec:

- **The three `[unexecuted]` rows in section 4.4 are confirmed by actually running them** under
  `python/.venv/Scripts/python.exe` before any test hard-codes an expected value, and the confirmed
  results are written back into this spec's table. Bash was disabled in the session that wrote it;
  the string-slicing rows are source-exact and the `int(s, 16)` grammar rows are not.
- **The `err == nil` guard is present and is pinned.** Section 5, and 11.4's M2 must have a non-empty
  RED set when actually run, not merely when predicted.
- **`TestEvents_TaskIDValidation`'s `?job_id=not-a-uuid` assertion is still green, unedited.** This
  is the check that the change is additive (section 6).
- **All eight prose sites in section 10 are corrected in the same commit as the handler change**,
  and `canonicalJobID` is **not** deleted (section 8).
- **The publisher census is re-derived, not inherited.** Section 5 rests on all eight JobID-carrying
  `broker.Publish` sites emitting `uuidStr(...)`. That is a claim about the complement, so the plan
  re-greps `events.Event{` in non-test Go and records the count it finds rather than quoting this
  spec's.
- **Gates:** `make test` (the new default-lane 11.3 runs here), `make test-integration` (11.1 and
  11.2), and `make test-race`. No Python gate - no Python code changes. No web gate - `web/` is
  untouched and does not send `job_id` (section 3).

---

## 13. Proposed backlog items

Proposals only. Not filed. The human accepts.

1. **`/v1/events` has no heartbeat, so a healthy idle stream and a wedged one are indistinguishable
   at every layer.** This slice removes the *most common* cause of a permanently silent stream and
   removes none of the others: a well-formed id for a job that does not exist, a job that is simply
   quiet, and a proxy that dropped the connection all present identically to a client with
   `read=None`. A periodic SSE comment frame (`: keepalive`) would let every client distinguish
   alive-but-silent from dead, and would stop intermediaries idling the connection out. Affects the
   Go CLI, the Python SDK, and the SPA's `task_id` streams equally. *(Type: feature. Priority:
   medium. Filing this is what makes section 6's "out of scope" a decision rather than an omission -
   the project's rule is that a decision conditioned on future work needs a findable item.)*

2. **`handleEvents` admits an unbounded caller-supplied string as a broker map key for the
   connection's lifetime.** Pre-existing, untouched by this slice, and narrowed by it only for
   strings that happen to parse. A megabyte-long `?job_id=` is retained per connection until the
   client disconnects; bounded only by connection count and `internal/api/ratelimit.go`. The likely
   remedy is a length cap before `Subscribe`, decided against the same asymmetry argument as G2 - cap
   without rejecting. *(Type: bug. Priority: low. Real but heavily bounded by the auth requirement.)*

3. **`internal/cli/logs.go` holds the sixth hand-written copy of the `uuidStr` format string, and
   `internal/cli/relayharness_integration_test.go` counts seven.** Its own comment says a change in
   the server's `uuidStr` "is caught by nothing: it is unexported, so no test relates the two". This
   slice adds no copy but makes the divergence matter more, since the CLI and the server must now
   agree on the canonical rendering for `jobSnapshotUnusable` to keep working. The remedy is a single
   exported renderer or a test that relates them. *(Type: chore. Priority: low.)*

---

## 14. Explicit scope boundaries

- **No validation or rejection of `?job_id=`.** G2, section 6. Item 1 above owns the general
  "silent stream" problem.
- **No Python production change**, and therefore no version bump. Section 9.
- **No deletion of `canonicalJobID`.** Section 8.
- **No change to `internal/events`.** The broker stays UUID-ignorant. Section 5.1.
- **No change to `?task_id=` handling**, which already validates, 404s, and canonicalises.
- **No heartbeat, no read timeout, no `follow_job` signature change.** Section 9 and item 1.
- **No change to `web/`.** It does not send `job_id`. Section 3.
- **No SDK response-shape or error-hierarchy work.** Owned by
  `bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy`.

---

## 15. Open questions the plan inherits

1. **The replacement wording for `canonicalJobID`'s comment.** Two of its four paragraphs are the
   `handleEvents` argument and become false; the surviving argument - `jobSnapshotUnusable` needs the
   canonical form, and argv must be canonicalised *before either request line is built* - is the one
   that keeps the function alive. Deleting the wrong paragraph leaves a function whose stated reason
   for existing is gone, which is how a future slice deletes it outright. The plan must decide the
   exact text, not leave it to the engineer.
2. **Whether `fakeUUIDSpellingServer` is relabelled or reworked.** Section 10 site 4 recommends
   relabelling: it models a pre-2026-08-30 server, and that server still exists in the field. If the
   plan disagrees, the change is larger than a comment and needs its own RED.
3. **Whether the section 4 table lands in the repo as a comment on `TestCanonicalJobIDSpellings`.**
   The recommendation is yes - a table of exact strings with the reason each is accepted or rejected
   is the artifact that stops this being rediscovered - but it is prose about behaviour, and this
   repo's dominant defect is prose about behaviour. If it lands, every row must be one the test
   actually exercises, so the test is the proof of its own comment.
