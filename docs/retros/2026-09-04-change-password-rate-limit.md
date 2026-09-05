---
date: 2026-09-04
topic: change-password-rate-limit
branch: claude/roadmap-now-dependencies-159be0
range: dca0035..85e5e4c
---

# Session Retro: 2026-09-04 - Change-Password Rate Limit

**TL;DR:** Changing your password makes the server do a deliberately slow calculation - about a fifth
of a second of one CPU core - and there was no limit on how often a logged-in user could ask for it.
One account could therefore keep every core on the machine busy indefinitely. This session added a
limit of five attempts per minute per user. Two things found along the way turned out to matter more
than the limit itself: the attack was twice as cheap as the bug report described, and one of the
tests written to prove the new limit worked would have passed even with the limit completely removed.

## Handoff

Lane A of the six-item Now batch. Closed `bug-2026-09-04-change-password-runs-bcrypt-cost-12-unlimited`,
merged as PR #202. `RELAY_PASSWORD_CHANGE_RATE_LIMIT` defaults to `5:1m`, a per-user bucket via
`UserRateLimit` on `PUT /v1/users/me/password` only, composed `auth(passwordLimit(h))` - the reverse
order 401s on request 1 because `UserFromCtx` sees a bare context. Measured on the reference machine
at cost 12 with a 28-byte password over 50 individually timed iterations: compare 185.3 ms median,
compare-plus-generate 368.9 ms; a review lens re-measured at 189.7 / 380.6. Two items filed rather
than fixed: `bug-2026-09-04-userratelimit-panics-on-a-zero-limit` and
`idea-2026-09-04-nothing-enforces-one-handler-call-per-server`. Admin bcrypt routes
(`password-reset`, `POST /v1/users`) stay unbounded by decision, recorded in README.

## What Was Built

- `internal/api/server.go` - `PasswordChangeLimitN`/`Win`, the limiter built beside `userLimit`, the
  route wrapped.
- `cmd/relay-server/{main.go,http_server.go}` - env parse, fatal on malformed, deps plumbing.
- `cmd/relay-server/password_ratelimit_wiring_test.go` - the default-lane suite, including the
  `go/ast` wiring guard.
- `internal/api/password_ratelimit_integration_test.go` - the burst and the success path.
- README's rate-limit row and the Session bullet.

## Key Decisions

- **A separate bucket from `RELAY_JOB_SUBMIT_RATE_LIMIT`.** Sharing one would either drop the submit
  ceiling to password-change rates or hand this route a ceiling far above what a human needs.
- **`5:1m`, sized on the mistype-retype run.** The item proposed "1 per few seconds"; that refuses the
  second attempt of a routine mistype, which is why the burst axis, not the sustained rate, set the
  number.
- **The item's step 3 - skip the compare for a recently-failed caller - was decided OUT**, because the
  cheaper attack loops SUCCESSFUL changes and a failure-conditioned gate bounds only the loop that
  produces failures.
- **Admin routes stay unbounded**, on the attacker-set argument (the admin table, not anyone who can
  create an account) rather than the "don't refuse an operator mid-incident" story, which is
  unfalsifiable.
- **A `go/ast` wiring guard was bought knowingly as an eleventh copy** of a family an open item says
  must not be pasted again, because zeroing main's deps literal leaves the whole lane green.

## What Went Wrong and What Changes

Ledger on the prior retro (`2026-09-04-agent-subprocess-to-task-logs-e2e`): "restoring shared infra is
itself a mutation" **applied** - every later brief in this batch carried the leave-it-running clause
and no further lane was reddened by it. "A lens's verdict and its evidence are two claims"
**recurred** - see the third entry below. Both are already promoted.

- **A shipped test proved nothing, and the rule against it was written twenty lines above it.** With
  the route unwrapped entirely, `TestChangePassword_ANormalChangeSucceedsUnderTheBucket` still passed.
  Its sibling in the same package says in so many words: "THE SIXTH REQUEST IS NOT OPTIONAL. Five
  400s under a limit of five are also what a limiter that does nothing produces." The rule existed,
  in the same file, and was not applied to the next test written.
  -> **What changes:** a test whose subject is a ceiling must include the request that is REFUSED,
  and the way to check is to ask what the test does with the control removed entirely - not with the
  control mis-configured. Getting a 204 from an unwrapped route and a 204 from a working one is the
  same observation. (promoted to [[reference_a_ceiling_test_must_assert_the_refusal]])

- **A guard bought to close one door did not look at the door next to it.** The `go/ast` wiring guard
  checks that the field is present, is a plain identifier, derives from the right env var and is
  assigned once. It says nothing about the `if err != nil { log.Fatalf }` three lines away - and
  deleting that block compiles, because `err` is a re-assignment. An operator typo like `5:1min` then
  turns a loud boot failure into a silently unarmed bcrypt bound, which is precisely the outcome the
  guard was bought to prevent.
  -> **What changes:** when a guard is justified by a specific failure ("the control could be off in
  production with CI green"), enumerate every path to that failure before writing it, not just the
  one the mutation you had in mind takes. Ask "what else makes this value wrong or absent?" - here,
  the answer was an unchecked error, not a mis-wired field.

- **A review lens's supporting measurement was wrong in the alarming direction, and the engineer
  caught it.** The lens reported `1:1ns` admitting 1623 requests/sec as evidence the window is a
  disguised off switch. That figure is the *clock's* tick rate, not the bound's - a 1 ns window admits
  once per distinct clock reading. The finding held; the number overstated it.
  -> **What changes:** already the rule, and it recurred. Worth adding one clause: a measurement that
  makes a finding *more* alarming deserves the same check as one that makes it less. The instinct is
  to scrutinise numbers that let you off.

- **The conductor's brief carried two factual errors into the fix round.** I told the engineer the
  false off-value claim was in the search README row (it was the submit row) and that the zero-limit
  panic surfaces as a 500 (it is a dropped connection with no status code at all, because `net/http`
  recovers and closes). Both were corrected by the engineer and one changed a backlog item I had
  already written.
  -> **What changes:** when relaying a finding into a brief, quote the reviewer's own file reference
  rather than paraphrasing which row or symbol it named. A paraphrase of a location is a fresh claim,
  and it is made without the evidence the original had.

## Recommended Backlog Items

Backlog intake, not a priority order.

- See [`bug-2026-09-04-userratelimit-panics-on-a-zero-limit`](../backlog/bug-2026-09-04-userratelimit-panics-on-a-zero-limit.md) - exported constructors with an unstated precondition; the failure is a dropped connection, not a 500
- See [`idea-2026-09-04-nothing-enforces-one-handler-call-per-server`](../backlog/idea-2026-09-04-nothing-enforces-one-handler-call-per-server.md) - every limiter built in `Handler()` is a fresh budget; only the search bucket is structurally unique

## Files Most Touched

- `cmd/relay-server/password_ratelimit_wiring_test.go` (+437 then +74) - the default-lane suite and
  the wiring guard, including the `log.Fatalf` row added in the fix round.
- `internal/api/server.go` (+62/-24) - the fields, the limiter, the route, and the `Handler` doc
  comment twice (the rewrite made a false claim about the search limiter and had to be narrowed).
- `cmd/relay-server/main.go` (+67/-20) - the parse and the deps literal; +43/-20 of that is gofmt
  realigning 20 pre-existing keys.
- `README.md` (+13/-8) - the rate-limit row, the false off-value claim in two rows, and the
  stolen-token recovery order.
- `internal/api/password_ratelimit_integration_test.go` (+163) - the burst and the (repaired) success
  path.
