---
date: 2026-08-25
topic: auto-enroll-guards
slice: auto-enroll-guards (two open bugs plus one fixture idea, shipped in one sitting)
branch: claude/pr-merging-session-868949
range: origin/main..HEAD (backend only; Go + one new SQL statement + `make generate`; no migration, no proto, zero files under `web/`)
pr: auto-enroll-guards - reference this work by date and slug, never by a predicted number
closes: bug-2026-08-12-auto-enroll-hostname-takeover, bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded, idea-2026-08-25-default-lane-fixture-for-the-enrollment-paths
amends: bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget, idea-2026-06-04-cidr-allowlist-auto-enroll
filed-this-slice: none
---

# Session Retro: 2026-08-25 - auto-enroll may create a worker and may never claim one; the security design passed four lenses on the first pass and the prose defending it took three remediation rounds, one of which was worse than the defect it replaced

**TL;DR:** `autoEnrollAndRegister` now refuses any hostname that already has a `workers` row, via
`InsertWorkerForAutoEnroll` (`ON CONFLICT (hostname) DO NOTHING RETURNING id`), so check and write are
one statement. `enrollAndRegister` gets a *different* guard - refuse only a non-NULL
`agent_token_hash` - because revoke-then-re-enroll is the recovery route and revoking nulls the hash.
`RELAY_AUTO_ENROLL_WORKER_CEILING` (default 1024, `0` disables) bounds token-less row creation, checked
inside the transaction before the insert. Refusals are counted, never logged. All eleven
`codes.Unauthenticated` returns on the registration path now pass one `msgAuthFailed` constant, held by
an AST guard that fails closed on a twelfth.

The behavioural design was not the expensive part. **Three prose remediation rounds were**, and the
second round's fix prescribed `relay workers delete` - a command that exists nowhere in the product.

---

## 1. The count, stated first, because it is the whole story

| Round | Instrument | Findings | Character |
|---|---|---|---|
| Phase 4 pre-fan-out | conductor-run `/code-review` | **3** | all prose or operator-facing message |
| Verify round 1 | four-lens fan-out (invariants, correctness, security, integration) | **~15** | prose, plus three surviving mutations and one lane regression |
| Verify round 2 (re-verify) | same fan-out on the fixes | **5** | including **the round-1 fix itself being wrong** |

The production security argument - create-only on the token-less path, nullability on the token path, a
ceiling before the insert, one refusal string - was **accepted on the first pass by every lens**.
Nothing about the guards' placement, predicate or fence relationship changed after Phase 3. What
changed, three times, was the text telling an operator what to do about them.

That asymmetry is now eleven consecutive iterations of this repository's dominant defect class, and this
slice is the sharpest instance yet, because both halves were measured against the same diff at the same
time by the same four lenses.

---

## 2. The headline: the round-2 fix was worse than the defect it replaced

**What shipped into review.** README, the agent's terminal exit message, and five places in the spec
justified design decisions by the cost of "deleting a worker, which destroys its assignments and
reservations", and told an operator to free a claimed hostname with `relay workers revoke`.

**What round 1 correctly found.** `revoke` does not free the hostname. `ClearWorkerAgentToken`
(`internal/store/query/workers.sql:83-86`) nulls `agent_token_hash` and sets `status = 'revoked'`; the
row stays, so `ON CONFLICT (hostname)` still fires and the create-only guard still refuses. The remedy
as written did not work.

**What the round-1 fix did.** It replaced `relay workers revoke` with `relay workers delete`, put it in
README twice and in the agent's terminal exit message, and **pinned it with a test asserting it was
"the only way to free the HOSTNAME"**.

**`relay workers delete` does not exist at any layer.** `internal/cli/workers.go`'s switch has no
`delete` arm - its usage line is `<list|get|disable|enable|revoke|workspaces|evict-workspace>`. There is
no `DELETE FROM workers` in `internal/store/query/`. The only DELETE route on the resource is
`/v1/workers/{id}/token`, which is revoke. It is refutable in one grep.

**And the item being implemented said so.** `bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded`
states in its own Summary that there is no `DELETE FROM workers` anywhere. The slice contradicted a fact
asserted by the document it was implementing, in the same session in which it had read that document.

**Even hypothetically, the cost claim was wrong three ways.** `tasks.worker_id` is `ON DELETE SET NULL`
(`000001:62`), so a running task would be orphaned rather than destroyed; `reservations.worker_ids` is a
bare `UUID[]` with no foreign key (`000001:89`), so reservations would be untouched entirely; and
`agent_enrollments.consumed_by` (`000005:9`) has **no `ON DELETE` action at all**, so the delete would
fail outright with a foreign-key violation for every worker ever enrolled with a token. The sentence
used to justify four decisions was false in its subject, false in its verb and false in each of its
three objects.

**The decisions all survived, and one got stronger.** The enrollment path's asymmetric guard was argued
as "otherwise delete is the only recovery". The truth is that there would be **no recovery at all** - a
revoked row that could not be re-enrolled would be permanently stuck. The argument was right for a
reason its author had not found.

### Why this is a finding rather than an embarrassment

A wrong claim about the world is ordinary. What makes this one worth a section is that it was
**produced by the correction process**, under review pressure, by a lane that had just been told the
previous sentence was unverified. The failure mode is specific: a reviewer refutes a claim, the fixer
feels an obligation to supply a *replacement* remedy, and the replacement is generated from
plausibility rather than looked up. The reviewer's finding was rigorous; the response to it inherited
none of that rigour.

> **A refutation creates pressure to name a substitute, and a substitute named under that pressure is
> the least-verified sentence in the document.** The moment after a review kills a claim is exactly when
> a fresh unverified claim enters, because the corrector is optimising for closing the finding rather
> than for being right. Treat every replacement as a NEW claim arriving at Phase 4 with no lens on it,
> not as the resolution of an old one.

---

## 3. Second consecutive slice where the correction of a claim was itself an unverified claim

The 2026-08-25 handler-pool-seam retro, written the same day, named this exact shape: *"the correction
of a uniqueness claim is itself a uniqueness claim, and inherits its unverifiability."* That slice's
round 2 introduced a false claim at four sites. This slice's round 2 introduced a ghost command at three
sites plus a test that pinned it.

The recurrence is data, not carelessness:

> **A lesson written down one slice earlier did not prevent its own recurrence one slice later, under
> review, in a session that had the lesson in context.** So the fix for this class is structural, not
> exhortative. A prose lesson fires when you are already looking for it, and correcting a review finding
> is precisely when you are looking at something else. This is the fourth recorded lesson in two slices
> to be violated after being recorded, and in every case the review fan-out was what caught it.

### What was built in response, and whether it generalises

Not another paragraph. A **negative guard**, `internal/agent/messages_test.go:139-143`:

```go
for _, ghost := range []string{"workers delete", "relay workers rm", "workers remove"} {
    assert.NotContains(t, msg, ghost,
        "this message must not prescribe a command that does not exist; add the subcommand to "+
            "internal/cli/workers.go first, then say so here")
}
```

The reasoning in the comment above it is the durable part: the surrounding test already asserted the
message *names a remedy*, with a battery of `assert.Contains`. **"The remedy is named" and "the remedy
exists" are different properties, and a substring check only tests the first.** Every positive
assertion in that test passed against the ghost command, and would pass against any plausible-looking
string.

**Does it generalise? Partially, and the limit should be stated rather than discovered.**

- **Where it does.** Anywhere a document or a message prescribes a command, a flag or an env var, the
  check "this identifier resolves in the product" is mechanical and is currently written nowhere in the
  tree. The negative form is the cheap 80%: enumerate the near-miss spellings you were tempted by, and
  forbid them.
- **Where it does not.** A deny-list of three ghost spellings is the shape CLAUDE.md already warns
  about; it fails **open** on the fourth. `relay worker delete` (singular) passes it today. The guard
  catches this mistake and its two nearest neighbours, not the class.
- **The property-level version exists and was not built.** Parse the message for `relay <noun> <verb>`
  and assert the verb appears in `internal/cli`'s dispatch for that noun - positive, exhaustive, maybe
  thirty lines. Declined, correctly: this repository measured the price of a structural guard eight days
  ago, and one exit message does not carry it.

Verdict: **the right size for the site and the wrong shape for the class**, and it is labelled as a
tripwire rather than as the property. Compare section 7, where the same slice took the same problem to
its exhaustive form because eleven sites justified it. The pair is instructive - one slice produced both
a deny-list tripwire and an AST-level exhaustive check, and the only difference between them is the
number of sites at risk.

---

## 4. The integration lane caught a regression no other lane could, and a conductor process error let it through

**The process error, owned here.** The conductor told the engineer to skip integration tests during
Phase 3. That is defensible on a slice with no migration and a fast default lane. It was wrong on this
one, and for a structural reason rather than a thoroughness one.

**What it let through.** Three pre-existing integration tests in `handler_tasklog_integration_test.go`
and `handler_taskstatus_integration_test.go` failed deterministically the first time the lane ran. They
had been sending a token-less `Register` for a pre-seeded hostname, and their own comments said why:
*"Auto-enroll upserts by hostname and returns the EXISTING row's id."* **They were green because of the
defect.** The takeover this slice existed to close was their setup mechanism.

Same shape as `TestConnect_AutoEnrollRotatesTokenForExistingHost`, which the slice had already planned
to invert - that one asserted the takeover as desirable in its own failure message ("re-enrollment
should rotate the agent token"). The spec found that one by reading. It did not find these three,
because they assert nothing about enrollment at all; enrollment is scaffolding inside their setup. **A
test that depends on a defect in its fixture is invisible to a search for tests that assert the
defect.**

### Which lane owns which class of evidence

- **The default lane owns branch reachability.** It proves a guard fires, that a refusal returns the
  right code, that no statement was issued. It cannot know what real Postgres does with `ON CONFLICT`,
  and it cannot know what else was relying on the old behaviour.
- **The integration lane owns the blast radius of a behaviour change.** Its unique product here was not
  "the SQL works". It was **the census of everything in the repository that had quietly come to depend
  on the removed behaviour.** No amount of default-lane coverage produces that census.
- **The four review lenses could not produce it either.** Every lens reads the diff. The three failing
  tests are *not in the diff*.

> **When a slice REMOVES a behaviour that other tests use as SCAFFOLDING rather than as SUBJECT, the
> only instrument that finds them is running them.** "Skip integration in Phase 3" is safe when a slice
> adds behaviour and unsafe when it removes one. The discriminator is not schema change and not
> duration; it is whether anything else in the tree could have been built on top of what you are
> deleting.

All four tests are now inverted or given real credentials, and the lane is green.

---

## 5. The item's prescribed remedy was on the wrong path, and striking an acceptance criterion was correct

Twenty-third consecutive iteration of "verify a backlog item's technical claims against the code", and
this one moved the code rather than merely confirming it.

**The refutation.** `bug-2026-08-12` proposed that `autoEnrollAndRegister` refuse when
`existing.AgentTokenHash` is non-NULL. On that path the predicate is **production-equivalent to "a row
exists"**: `agent_token_hash` has exactly two writers, `CreateWorker` has no production caller (twenty
call sites, every one in a `_test.go`), and a revoked row was already refused three lines earlier. The
item's narrow-sounding predicate did the wide thing while reading as something else, and it retained a
status deny-list (`existing.Status == "revoked"`) that fails **open** on the next status added.

**The predicate was not wrong; it was on the wrong path.** On the *enrollment-token* path, NULL and
non-NULL are genuinely different products: NULL means revoked, which is the recovery route, and
non-NULL means a live credential, which is takeover. The spec adopted the item's predicate verbatim
there and took the honest wide spelling on the auto-enroll path. One item, two guards, and the asymmetry
is forced rather than chosen.

### Is striking an acceptance criterion a legitimate spec move?

Item 1's criterion 2 read: *"A revoked worker's hostname still re-enrolls (`agent_token_hash` is NULL
after revocation), so the legitimate recovery path is not broken."* Implemented literally on the
auto-enroll path that **revives revoked workers token-lessly** - the opposite of the item's own purpose,
and a regression against the existing check in `handler.go`, against
`TestConnect_AutoEnrollRefusesRevokedWorker`, and against README:364-368 which documents the
non-revival as deliberate.

**Yes, it was legitimate. What made it safe was not the reasoning - it was the receipts.** The move is
defensible under three conditions, all met here, and worth naming because the dangerous version looks
identical from outside:

1. **Refuted against artefacts that predate the item** - code, a green test, and a documented decision -
   rather than against the spec author's preference. Three independent witnesses, all older than the
   criterion.
2. **A replacement written in the same breath, at the same specificity.** The revoked *hostname* stays
   refused on auto-enroll and the revoked *worker* stays revivable by an admin-issued enrollment token,
   each pinned by a named test that must stay green **and unedited**. A struck criterion with nothing in
   its slot is scope reduction wearing a spec's clothes.
3. **Recorded in three places a future reader lands** - as decision D2, in the spec's acceptance list,
   and in the item's `## Resolution` note. The item does not close claiming a criterion was met.

The same discipline applied to the fixture item, whose `errWorkerRevoked` criterion was **reshaped, not
met**: the sentinel ceased to exist, so its named branch cannot be asserted, and the Resolution note
says exactly that instead of ticking the box.

---

## 6. Three mutations survived because the fixture could not distinguish WHERE a statement was issued

The mutation was `txq.X -> h.q.X` on each of the three guards - the create-only check, the ceiling
check, and the enrollment path's `FOR UPDATE` lookup. That hoists each guard **out of its transaction**
onto the pool. All three survived the battery.

**Why nothing noticed.** The fixture had one `seen` list, appended to by both the pool fake (`strandDB`)
and the transaction fake (`fakeTx`). A statement issued on the pool and a statement issued inside the
transaction produced byte-identical evidence, so every assertion of the form "the guard ran" was
satisfied by a guard running in the wrong place.

**What that cost, as a property rather than as a mutation score.** Hoisting the ceiling check out of the
transaction reopens the TOCTOU the design closed. Hoisting the enrollment lookup out drops the
`FOR UPDATE` lock that makes the live-credential check non-racy - and the handler comment claiming *"the
`FOR UPDATE` lock is what makes this non-racy"* had **no witness anywhere in the tree**. True and
unenforced, which is the recorded "a principle in a comment is not a check" pattern arriving from the
fixture side rather than the production side.

**The fix.** An owner tag supplied by whichever fake receives the call
(`handler_enroll_guards_test.go:100`, `owner string // "tx" for fakeTx, "pool" for strandDB`), recorded
on every `scriptedQuery` at `:111`, and read back through `sawStatementOn(owner, substr)` at `:201`.
Assertions now name the owner, so a hoisted statement is a *different observation* rather than the same
one.

> **A fake that merges two call sites cannot pin the difference between them.** If two production seams
> funnel into one recorder, every property depending on *which* seam was used is unfalsifiable, and the
> tests look thorough while being silent about it. The tell is a single shared slice on a fixture
> standing in for more than one seam. Tag the observation with its source **at the point of recording**,
> not at the point of assertion, where the information is already gone.

Close relative of the recorded "same-typed adjacent args transpose silently": both are guards that prove
something happened and are fail-open on *which* something.

---

## 7. An exhaustiveness claim was converted from prose into a check

README asserted that every credential refusal on the gRPC registration surface returns the identical
string. **The slice's own `"auto-enroll disabled"` refuted it**, and so did `"worker revoked"`, which was
additionally a hostname-state oracle: it told an unauthenticated caller that a row for that hostname
exists and is revoked.

Round 1's fix reworded the README sentence. That is the weakest available response and it did not
survive re-verification, because the property was still held by coincidence - five separately-typed
string literals happening to agree, with a test table claiming to be "exhaustive BY CONSTRUCTION" while
driving 5 of 11 sites and admitting in its own next clause that a new arm is "only caught if it is added
here too".

Round 2 made it structural:

- One `const msgAuthFailed = "authentication failed"` (`internal/worker/handler.go:57`) at all eleven
  `codes.Unauthenticated` returns on the registration path. `"worker revoked"` was **replaced**, not
  joined - leaving it beside a generic refusal would have let a caller separate "revoked" from "taken"
  by message, which both items forbade.
- `TestRegistrationRefusals_AllUseTheSharedConstant` (`internal/worker/refusal_string_guard_test.go`)
  parses `handler.go` and requires the message argument of every such call to be the **identifier**
  `msgAuthFailed`. A `BasicLit` is a site that can drift; a different `Ident` is "a second constant,
  which is the same defect wearing a better costume".
- A separate `unauthenticatedRefusalSites = 11` tripwire, labelled in its own comment as **not** the
  property. The AST half makes a twelfth site indistinguishable automatically; the count exists only to
  force the question of whether the new site is a new *outcome* the refusal table needs an arm for.

> **An exhaustiveness claim in prose is a claim about a set the prose cannot enumerate, and the fix is
> not better prose.** Route the claim through a single symbol and let a check assert the symbol is the
> only route. The guard then fails closed on the site that has not been written yet, which is the only
> site that matters.

The guard also labels which of its two halves is load-bearing - the SUBSTITUTE/PERMANENT discipline the
previous slice's retro proposed, applied on the day it was proposed.

---

## 8. A remedy that favours the attacker, twice, in one README section

Two instances of the same shape, and it is the shape CLAUDE.md already records for
`task_status_fence.counts.conflicting_total`.

**Instance 1: remedy 1 is the loop's engine.** README's ceiling ladder prescribes "revoke the junk"
first, because `CountWorkers` excludes revoked rows so revoking frees budget immediately. Under an
*active* attacker that is a treadmill: revoking frees the budget without freeing the **hostname**, the
attacker refills with new hostnames, the old ones stay claimed forever, and the table grows without
bound in the revoked bucket while the counted total sits flat. The operator's compliance with the
documented remedy is what keeps the loop running. README now says both halves, and "Row growth is
bounded" was corrected to "**NON-REVOKED** row growth is bounded; total row growth is not".

**Instance 2: `0` sat in the ladder as a peer option.** Setting the ceiling to `0` disables the bound.
It was originally listed as a numbered step alongside "revoke" and "raise the ceiling". A climbing
`fleet_at_ceiling` count is **exactly the signal an attacker filling the budget produces**, and
disabling the bound is exactly what that attacker wants. It has been demoted out of the ladder into its
own paragraph (README:441-446) that says so explicitly and cross-references the `conflicting_total`
precedent.

> **When a signal has a documented remedy, ask what a peer who can MOVE that signal gains from the
> operator following it.** This is the second slice in a row to find an instance, and it is now two for
> two that the counter was fine and the *advertisement* was the defect. The check belongs where the
> signal is READ, in the commit that ships it.

The README also gained a step 0 that did not exist in the spec: read the `auto-enrolled worker` audit
lines, which carry hostname and remote address and are "the only attributable signal this system
produces". The counters say how many and why; only the audit line says **who**. That is a genuinely
better first step than any of the three remedies, and it emerged from the same review pass that
demoted `0`.

---

## 9. One accepted trade the conductor owns

The slice added `registrationStoreFault` (`internal/worker/handler.go:88-141`), which turns a store
error anywhere on the registration path into `codes.Internal, "registration failed"` for the peer and
puts the detail in the server log. It closes a real disclosure: both enrollment transactions previously
returned the wrapped error verbatim, and with no sanitizing interceptor grpc-go sent the full text as
`codes.Unknown` - a table name, an index name and a SQLSTATE to a caller that presented no credential,
and a refusal distinguishable by **both** code and message from the three README says are identical.

**The engineer hesitated**, on the grounds that this slice's stated position was "no new log site at
all", which was the whole argument in section 7 of the spec for counting refusals rather than logging
them. **The conductor accepted the trade**, reasoning that this is a *fault* site rather than a
*refusal* site - a refusal is the system working and is counted; a store fault is an operator-actionable
server condition that must not be silent - and that it is bounded at one line per stream, the same as
the audit line.

**Review then showed the justification was incomplete in two ways**, both now written into the comment
at `:114-132` in the shipped tree:

- **Parity with the audit line is false.** Both are one line per stream, priced by
  `RELAY_GRPC_MAX_CONNS` and `RELAY_GRPC_REGISTRATION_TIMEOUT`, and neither is covered by the
  per-connection ingest budget (not allocated until after registration). But the audit line has a
  **second** bound this one does not: it fires only on success, so after the create-only rule it is one
  line per hostname *forever*, additionally capped by the ceiling. `registrationStoreFault` has no
  per-hostname bound at all.
- **It is reachable pre-authentication.** A peer presenting no credential and a hostname over the
  roughly 2704-byte btree entry limit fails the `workers.hostname` unique index deterministically, every
  time - and because `internal/agent` treats only `codes.Unauthenticated` as terminal, the caller
  reconnects on backoff rather than exiting. The honest description is "unbounded by hostname, bounded
  only by the connection caps, and driveable by an unauthenticated caller".

**The decision stands. The justification needed correcting, and this is a conductor call rather than an
engineer error.** The engineer's hesitation was the correct signal and was overruled on reasoning that
was 80% right; the missing 20% is what the fan-out supplied. Two things follow. First, the disclosure
fix is worth more than the log site costs, and the alternative (return sanitized without logging) trades
an operator-visible fault for a silent one, which this project has spent five slices arguing against.
Second, **the hostname-bound item that would close the pre-auth reachability is now load-bearing rather
than nice-to-have** - it is proposed below, and section 9 is why its priority is not `low`.

> **When a conductor overrules an engineer's scope hesitation, the reasoning that overruled it is itself
> an unverified claim and should be routed through the fan-out like any other.** Here it was, and it
> came back partially refuted.

---

## 10. One live prose defect found by reading in this pass, and it is in the paragraph that fixed section 2

`README.md:420-422` currently reads:

```
layer, so revoked junk rows are permanent. Bounding the table itself, and reaping those rows, is what
that worker's assignments and reservations. Bounding the table itself is not something this ceiling
does or is trying to do.
```

The second sentence has **no predicate**: "is what" is followed by "that worker's assignments and
reservations." It is the residue of the edit that removed the `relay workers delete` claim from section
2 - the old text read something like "...is what a TTL reaper would do, and deleting a worker would
destroy that worker's assignments and reservations" - and the removal took the verb with it. "Bounding
the table itself" then appears twice in three lines.

**This is the eleventh-iteration instance, and its position is the point.** It is not in a stale
comment or a forgotten doc. It is in the exact paragraph that the round-2 remediation rewrote *in order
to correct a false claim*, and it survived a third review round because every lens was checking whether
the paragraph's assertions were TRUE and none was checking whether its sentences were WELL-FORMED.

**It is a one-to-two-line conductor fix before the PR**, listed here as a finding rather than an item.
The replacement should say what the paragraph is trying to say: bounding the table itself, and reclaiming
those rows, is what a reaper would do, and this ceiling does not do it or try to.

> **A prose edit that DELETES a clause can strand its neighbours.** The failure is invisible to a truth
> check, because a fragment asserts nothing. Re-read the whole paragraph after removing a sentence from
> the middle of it, not just the sentence you replaced.

---

## What Was Built

- **`internal/store/query/workers.sql`** - `InsertWorkerForAutoEnroll`
  (`ON CONFLICT (hostname) DO NOTHING RETURNING id`, a sqlc `:one`, so a taken hostname surfaces as
  `pgx.ErrNoRows`), plus corrected comments on `UpsertWorkerByHostname` (its only caller is now
  `enrollAndRegister`; the old comment claimed it refreshed specs "on reconnect", which reconnect never
  did) and `SetWorkerAgentToken`. `make generate` re-run under the CRLF procedure.
- **`internal/worker/handler.go`** - the create-only guard and the ceiling inside
  `autoEnrollAndRegister`'s transaction (ceiling first, so a refusal writes nothing); the
  `FOR UPDATE` + non-NULL-hash guard as the first statement of `enrollAndRegister`'s existing
  transaction; `msgAuthFailed`; `errHostnameClaimed` and `errFleetAtCeiling` replacing
  `errWorkerRevoked`; `registrationStoreFault`; `AutoEnrollWorkerCeiling *int` with
  `DefaultAutoEnrollWorkerCeiling = 1024` and a resolver whose zero is meaningful (unlike
  `RegistrationTimeout`'s, which is why the field is a pointer).
- **`internal/worker/autoenroll_refusal_counters.go`** - refusal counts split by reason
  (`hostname_claimed`, `fleet_at_ceiling`, `credential_live`), modelled on
  `taskstatus_fence_counters.go`, read through an accessor returning a snapshot by value.
- **`internal/worker/handler_enroll_guards_test.go`** (new, no build tag) - the default lane's first
  route to both enrollment paths, with the four fixture blockers closed: statement-discriminating
  `QueryRow` on `strandDB` and on `fakeTx`, a configurable `Exec` command tag, an enrollment-row stub
  with a future `ExpiresAt`, and the owner tag of section 6.
- **`internal/worker/refusal_string_guard_test.go`** (new) - the AST guard of section 7.
- **`internal/agent/messages.go` / `messages_test.go`** - the token-less exit message now names all
  three causes and real remedies, with the ghost-command guard of section 3.
- **`cmd/relay-server`** - env parse, unconditional startup bounds line naming the effective ceiling and
  saying explicitly when it is disabled, following `parseWatchdogDuration`'s three-outcome shape, never
  `log.Fatalf`.
- **`README.md`** - six falsified sentences corrected, including "Nothing bounds the total.", plus the
  operator ladder, the narrow reading of the ceiling's promise, and the first-boot disclosure.
- **Four existing tests inverted or re-credentialed** (section 4). **Three backlog items closed.**

## Key Decisions

- **Auto-enroll refuses on "a row exists", not on "the hash is non-NULL"** - equivalent in production,
  fails closed on states that do not exist yet, and deletes a status deny-list rather than editing one.
- **A new SQL statement, not a Go predicate.** `SELECT ... FOR UPDATE` on a hostname that does not exist
  locks nothing, so two concurrent first-boots of the same fresh hostname both see no row and
  `DO UPDATE` lets the loser overwrite the winner's freshly minted token. One statement, no window.
- **The two paths get different guards, and the difference is forced** by the recovery route, not chosen
  for symmetry.
- **The ceiling gates the auto-enroll path only**, which is what makes it answerable to the
  denial-primitive objection without shipping a reaper: enrollment tokens are never refused by it.
- **The bound is approximate and every site says so** - `ceiling + RELAY_GRPC_MAX_CONNS`, never
  `ceiling`. Exactness would need serializable isolation on a hot path.
- **Refusals are counted, never logged.** A refusal is unboundedly repeatable by the same caller, and
  the per-connection log limiter is not allocated until after registration.
- **The counters section on `GET /v1/server/counters` was de-scoped at plan time**, on measured evidence
  (`internal/api/server_counters.go` is 574 lines with a 1355-line test). Counters live on `Handler`
  with an exported accessor; README says the publication is deferred.
- **`"worker revoked"` deleted rather than joined** - it was a live hostname-state oracle and no test
  asserted the string.

## Findings Triage

- **1 HIGH, ghost command** (section 2): `relay workers delete` in README twice, in the agent's terminal
  exit message, and pinned by a test - introduced by a *fix*, refutable in one grep, and contradicting
  the item being implemented. Now closed by a negative guard.
- **1 HIGH, lane regression** (section 4): three integration tests green because of the defect,
  invisible to every reading-based instrument.
- **1 HIGH, pre-auth disclosure**: both enrollment transactions returned raw wrapped store errors to an
  unauthenticated peer as `codes.Unknown`. Closed by `registrationStoreFault`.
- **3 surviving mutations** (section 6): all three guards hoistable out of their transactions with no
  test noticing. Closed by the owner tag.
- **1 MEDIUM, remedy favours the attacker** (section 8), in two instances.
- **1 MEDIUM, exhaustiveness by coincidence** (section 7): a table claiming to be exhaustive by
  construction that drove 5 of 11 sites.
- **~20 prose defects across three rounds** (3 + ~15 + 5). **One survives in the shipped tree**
  (section 10) and is a conductor fix.

## What Remains Open

- **Relay cannot delete a worker at any layer.** Discovered by this slice rather than created by it, and
  the create-only rule makes it operationally load-bearing: for one case the operator loop does not
  terminate. Item below.
- **The first-boot lockout window.** The auto-enroll transaction commits the row and its minted
  `agent_token_hash` before the `RegisterResponse` is sent and before the agent persists it, so a failed
  persist claims the hostname with a credential the agent never received - refused thereafter by both
  paths. **Before this slice the retry self-healed**, because the upsert rotated the token, so closing
  the takeover is what makes it permanent. Disclosed in README, not closed. Item below.
- **The ceiling bounds `CountWorkers`, not the table** (section 8). The reaper is the complement. Item
  below.
- **`reg.Hostname` has no length or charset bound**, deliberately scoped out, and it is what makes
  `registrationStoreFault` pre-auth reachable (section 9). Item below.
- **Refusal counters are not on `GET /v1/server/counters`.** De-scoped at plan time; README says so.
  Item below.
- **`fleet_at_ceiling` aliases the other two reasons at capacity.** The ceiling check precedes the
  insert deliberately, so at capacity every token-less refusal is attributed to the ceiling and
  `hostname_claimed_total` goes flat exactly when an operator starts triaging. Ordering is correct;
  aliasing is disclosed where the signal is read.
- **M10 is a known survivor**: moving `enrollAndRegister`'s lookup outside its transaction is not
  behaviourally detectable even after the owner tag, because the tag distinguishes tx from pool but the
  hoisted-lookup variant still issues on the tx. Recorded so nobody later "fixes" it.
- **No local `-race` path works on this host** - unchanged from yesterday, item already proposed there.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, twenty-third iteration. Three
  load-bearing claims across the two items did not survive, and two of the three moved the code.
- **A backlog proposal is not a contract** - twenty-three for twenty-three. The prescribed predicate was
  right on a path the item did not name.
- **An accurate item can prescribe a wrong remedy** - the clean instance: item 1's diagnosis was correct
  and its predicate belonged on the other path.
- **Each stage treats the previous stage's output as untrusted** - honored. The spec refuted the item,
  the plan reduced the spec's counters scope on measured evidence, and the fan-out refuted the spec's
  own operator prose five times.
- **Verify the mutation actually applied** - honored, and it mattered: **two mutations silently failed
  to apply on the first attempt (CRLF) and reported false survivals** until re-run. That is now five in
  this tree.
- **A mutation proof must leave a test behind** - honored; the owner tag and its assertions are
  permanent.
- **Say "declined, and here is the price"** - honored for the reaper, the CIDR allowlist, hostname
  validation and the counters section, each with the price written down.
- **Wrong prose about correct code is the dominant defect class** - **eleventh consecutive iteration**,
  three rounds deep, and one instance still shipped.

New from this iteration:

- **A refutation creates pressure to name a substitute, and that substitute is the least-verified
  sentence in the document.** Route it through the fan-out as a new claim. **Candidate for durable
  memory.**
- **"The remedy is named" and "the remedy exists" are different properties.** A substring assertion
  tests only the first; the check for the second is negative or AST-shaped. **Candidate for durable
  memory.**
- **A fake that merges two call sites cannot pin the difference between them.** Tag the observation with
  its source at the point of recording. **Candidate for durable memory.**
- **When a slice REMOVES a behaviour, run the integration lane** - other tests may use it as scaffolding
  rather than as subject, and that dependency is invisible to every reading-based instrument.
- **A prose edit that deletes a clause can strand its neighbours**; a truth check cannot see a fragment.
- **When a conductor overrules an engineer's scope hesitation, the overruling reasoning is itself an
  unverified claim.**

## Files Most Touched

- `internal/worker/handler.go:43-141` - the sentinels, `msgAuthFailed`, and `registrationStoreFault`
  with the corrected bound argument of section 9. Where the next person to touch a refusal lands.
- `internal/worker/handler.go:760-825` - `autoEnrollAndRegister`'s transaction: ceiling, then insert,
  then the two `errors.Is` arms mapping both refusals onto one string.
- `internal/worker/handler_enroll_guards_test.go:100-210` - the owner tag and `sawStatementOn`. The
  section 6 story is legible from this block alone.
- `internal/worker/refusal_string_guard_test.go:13-70` - the AST guard, and the comment distinguishing
  the tripwire from the property.
- `internal/agent/messages_test.go:125-143` - the ghost-command guard and the reasoning for why it is
  negative.
- `README.md:414-452` - the narrow reading of the ceiling's promise, the ladder, the demotion of `0`,
  **and the malformed sentence of section 10**.
- `docs/superpowers/specs/2026-08-25-auto-enroll-guards.md:1132-1190` - the dated correction block. Worth
  reading as a worked example of a spec recording that it was wrong rather than being quietly edited.

## Verification

- **This pass had no shell.** Nothing was executed here - no `git log`, no `git diff`, no test run. Every
  claim below that could be checked by reading was checked against the worktree.
- **Confirmed against code, not inferred:** that `msgAuthFailed` exists at `handler.go:57` and is used at
  eleven `codes.Unauthenticated` sites; that `errHostnameClaimed` and `errFleetAtCeiling` replaced
  `errWorkerRevoked`; that `AutoEnrollWorkerCeiling` is a `*int` with a resolver treating negative as
  default and `0` as disabled; that `registrationStoreFault`'s comment carries both the parity
  correction and the pre-auth reachability disclosure; that the AST guard requires the `msgAuthFailed`
  identifier and carries an 11-site tripwire labelled as not-the-property; that the ghost-command guard
  is `assert.NotContains` over three spellings; that the owner tag exists on `rowScript` and is read
  through `sawStatementOn`; that README's ladder demotes `0` out of the numbered steps and adds a step 0
  naming the audit line; and that `README.md:420-422` is malformed.
- **Reported by the implementing and verifying lanes, not re-run here:** all 21 unit packages green;
  `internal/worker` green at `-count=10`; integration green on a cache-busted run across
  `internal/worker` (140.8s), `internal/store` (180.8s) and `cmd/relay-server` (25.5s), plus
  `internal/api` and `internal/agent`; `go vet` and `go vet -tags integration` clean; a mutation battery
  across both rounds with applied-checks and controls.
- **`go test -race` was unrunnable locally all session** - ThreadSanitizer allocation failure,
  environmental, reproduced at `origin/main` on an untouched package. CI's `race + integration-build`
  job is the gate and this slice relies on it. Second consecutive slice in this position.
- **Not verified here:** all test results, the commit set, the diff stat, and the change set as `git`
  sees it.
- **No PR number appears anywhere in this retro or in the proposed items**, by instruction.
- **Outstanding and belonging to the conductor:** the README:420-422 fix, the item filings below, the
  final gates, all commits, and a ROADMAP refresh.

## CLAUDE.md verdict

**One amendment is earned, and it is a sentence rather than a bullet.**

CLAUDE.md's Invariants already carry "Identity is not honesty" and the `conflicting_total` worked
example, which say to ask, where a signal is read, what a peer who can move it gains from the
documented remedy. Section 8 is the second independent instance in two slices, and the *second* half of
it is new: **an option that disables the control belongs outside the remedy ladder, not inside it as a
peer.** That is a concrete, checkable rule and it is not currently implied by the existing text. Suggest
appending one clause to the existing bullet rather than adding a new one.

**Not earned, and deliberately so:**

- The prose lessons of sections 2, 3 and 10 have no code-facing form and CLAUDE.md's Invariants section
  is for rules that new *code* must not bypass. Durable memory, proposed above.
- The fixture lesson of section 6 is a test-design rule. The same call was made yesterday for the same
  reason.
- **`RELAY_AUTO_ENROLL_WORKER_CEILING` does belong in README** and is there; it does not belong in
  CLAUDE.md, whose env-var coverage points at README by design.

One thing the conductor should consider and I do **not** recommend: adding the create-only rule to the
Invariants list. It is a property of one code path with a guard and two default-lane tests, not a
cross-cutting rule other code could bypass. The invariant it actually strengthens - the epoch fence and
its `worker_id` predicate - is already there, and this slice is upstream of it rather than a peer.

## Recommended Backlog Items

Proposals only. The conductor files via `/backlog` and the human gives final accept. **Five candidates
were weighed; four are recommended and one is rejected with reasons.**

**1. `bug`: relay cannot delete a worker at any layer, and one operator loop does not terminate** -
priority `high`.

- **The gap is specific and reachable, not theoretical.** A machine re-provisioned in place under
  auto-enroll, whose operator will not or cannot get an enrollment token issued, has **no remedy**:
  auto-enroll refuses it (a row exists), the enrollment path refuses it (the credential is live), revoke
  frees budget but never the hostname, and there is no delete. Renaming the host is the only escape and
  it is not always available.
- **It is filed as `high` rather than `medium` because this slice created the terminal case.** Before
  the create-only rule, the upsert rotated the token and the loop self-healed. The retro discloses it;
  the item is what closes it.
- **The FK work is the real content and must be in the item.** `agent_enrollments.consumed_by`
  (`000005:9`) has **no `ON DELETE` action at all**, so a naive `DELETE FROM workers` fails outright with
  a foreign-key violation for every worker ever enrolled with a token. `tasks.worker_id` is
  `ON DELETE SET NULL` (`000001:62`), so running tasks orphan rather than cascade - which needs its own
  decision. `reservations.worker_ids` is a bare `UUID[]` with no foreign key (`000001:89`), so
  reservations are not cleaned up by the database at all and need an explicit sweep.
- Acceptance should require the migration decision on `consumed_by` to be stated (SET NULL versus
  RESTRICT), a refusal-or-cascade decision for non-terminal tasks, and the CLI/route/query triple. It
  should **also require deleting the ghost-command guard** in `internal/agent/messages_test.go`, since
  the guard exists precisely to hold this gap open until the command is real.

**2. `idea`: publish the auto-enroll refusal counters under `auto_enroll` on `GET /v1/server/counters`** -
priority `medium`.

- The counters exist on `Handler` with an exported accessor and three reasons; only the HTTP section is
  missing. De-scoped at plan time on measured evidence (`internal/api/server_counters.go` is 574 lines
  with a 1355-line test, and `cmd/relay-server/counters_wiring_test.go:242` asserts the served section
  count), and README already tells the reader it is deferred - so the item closes a documented gap
  rather than opening a new question.
- Acceptance is the established six-part payload checklist plus a section-count bump, and a test reading
  all three reasons back through the endpoint. **The item should carry the `fleet_at_ceiling` aliasing
  note**: at capacity every token-less refusal is attributed to the ceiling and `hostname_claimed_total`
  goes flat, and that must be documented where the section is read.

**3. `idea`: reap auto-enrolled worker rows that never completed a post-enrollment reconnect** -
priority `medium`.

- The ceiling's complement, and the only option that helps a deployment already hit. Without it the
  operator's documented remedy is a treadmill under active attack (section 8).
- **The schema problem is the item's centre and it should lead with it: nothing records which path
  created a row.** `connection_epoch <= 1 AND status != 'online'` is the nearest available proxy and it
  also catches token-enrolled machines that legitimately have not returned. Adding a column is probably
  the honest answer and that is a migration.
- Must decide `revoke` versus `delete` as the reaper's action. `revoke` frees ceiling budget, is
  non-destructive, is what an operator does by hand today, and needs none of item 1's FK work - so this
  item is **not** blocked on item 1 if it takes the revoke arm. Say that explicitly, or the two will be
  sequenced unnecessarily.

**4. `bug`: `reg.Hostname` has no length or charset bound, and that is what makes the new fault log site
pre-authentication reachable** - priority `medium`.

- **Not `low`, and section 9 is the reason.** The spec scoped hostname validation out when the only cost
  was a possible Postgres error disclosure. That disclosure is now closed by `registrationStoreFault`,
  but the same oversized hostname now deterministically drives an **unbudgeted server log line from an
  unauthenticated caller**, forever, because `internal/agent` treats only `codes.Unauthenticated` as
  terminal so the caller reconnects on backoff rather than exiting. The comment at `handler.go:124-132`
  says all of this and calls itself "the disclosure rather than the fix".
- Two concrete things to establish, both currently unverified: whether the `workers.hostname` unique
  btree (max entry roughly 2704 bytes) is in fact what rejects an oversized hostname, and where the bound
  belongs - a length check ahead of the transaction is the cheap arm, a `CHECK` constraint is the
  thorough one and is a migration.
- Acceptance should require a default-lane test that an over-length hostname is refused **as
  `codes.Unauthenticated` with `msgAuthFailed`**, so it joins the indistinguishable set rather than
  becoming a twelfth outcome.

**5. `idea`: close the first-boot lockout window with a server-side notion of an unconfirmed first
registration** - priority `medium`.

- `autoEnrollAndRegister` commits the row and the minted `agent_token_hash` before the
  `RegisterResponse` is sent and before `creds.Persist` runs on the agent. If the stream dies in that
  window, or the state directory is read-only or full, the hostname is claimed with a credential the
  agent never received, and both paths refuse it thereafter.
- **This slice created the permanence and the item should say so**: before the create-only rule the
  retry self-healed via the upsert's token rotation. That framing is what stops a future reader treating
  it as an inherited wart.
- It is genuinely a design question - the row is not really the agent's until the agent holds the token -
  which is why it was disclosed rather than closed. Plausible shapes to weigh in the item: a
  `first_seen_confirmed` flag cleared until the agent's first authenticated reconnect, versus letting
  auto-enroll rotate the token for a row that has never had one. **Note the trap on the second shape:**
  "has never had a successful reconnect" is exactly the state an attacker's junk row is in, so that arm
  reopens a narrowed form of the takeover and needs a bound of its own.
- Overlaps item 3 in the column it would need, and the two should be written to share it.

**Rejected, with reasons:**

- **An item for the prose-failure class of sections 2, 3 and 10.** Rejected. There is no acceptance
  criterion a future slice could satisfy and close, so it would be a permanent open row - the same call
  made yesterday for the same reason. The lessons belong in durable memory (proposed above), the
  `0`-in-the-ladder half belongs as a clause on CLAUDE.md's existing bullet, and the one item-shaped
  residue is the README:420-422 fragment, which is a conductor fix in this session and not an item at
  all.
