---
date: 2026-09-04
topic: change-password-rate-limit
item: docs/backlog/bug-2026-09-04-change-password-runs-bcrypt-cost-12-unlimited.md
status: draft, pending human review
---

# A per-user bucket on `PUT /v1/users/me/password`, the one bcrypt route with no bound

## Status of this document

Written by the TPM agent from tree evidence. The brainstorming flow's one-question-at-a-time dialogue
was not available in a subagent session, so every design question below is resolved against the tree
and each answer carries the evidence that settled it.

**Nothing here was measured by running it.** The Bash tool was disabled in the authoring session, so
the one figure this slice most wants - the wall-clock cost of a cost-12 bcrypt compare on this
machine - could not be taken. Section "Provenance of every number" says, per figure, which are read
from the tree, which are re-derived arithmetic, and which are quoted from a document without
verification. Section "The measurement this slice owes" makes taking it a precondition of merge, and
says what it can and cannot change.

Predecessor slice: `docs/superpowers/specs/2026-09-04-authenticated-route-rate-limiting.md`. Its
keying argument, its `UserRateLimit` construction rules, its `Retry-After` finding and its
named-field trade at the `api.New` boundary are settled there and are cited rather than re-argued.
That slice deliberately left this route out and its retro
(`docs/retros/2026-09-04-authenticated-route-rate-limiting.md`) filed the item this spec answers.

## 1. The slice, in one paragraph

One new user-keyed rate-limit bucket wrapping exactly one route,
`PUT /v1/users/me/password`. It reuses `UserRateLimit` and `ParseRateLimit` in
`internal/api/ratelimit.go` unchanged. It adds one env variable, two `api.Server` fields, two
`httpServerDeps` fields, two `buildHTTPServer` assignments, one route wrap, and a wiring guard. It
changes no handler. It corrects three pieces of prose that this change makes false or incomplete.

## 2. What the item gets right, and the five things this spec refutes

The item was read once for self-contradiction, contradiction with the tree, and prescription of
things that do not exist, before any design was written. Checks run and their outcomes:

**Confirmed against the tree**

- `internal/api/server.go:173` registers `mux.Handle("PUT /v1/users/me/password", auth(http.HandlerFunc(s.handleChangePassword)))`
  - bare `auth(...)`, no limiter. True.
- `internal/api/auth.go:308` runs `bcrypt.CompareHashAndPassword` against the caller's stored hash on
  every request that gets past two cheap guards. True.
- `bcryptCost = 12` at `internal/api/auth.go:23`, and every hash-generating site in the package uses
  it, so a production-created user's stored hash carries cost 12 and `CompareHashAndPassword` (which
  reads the cost out of the hash, not out of the package variable) does 2^12 key-expansion rounds.
  True.
- `POST /v1/auth/login` is wrapped in `RateLimit` and runs a bcrypt compare on every attempt
  including the `getDummyHash()` path. True. The comparison the item draws holds.
- The repro is reachable: a wrong `current_password` reaches the compare and is refused with 403,
  changing no state.

**Refuted or corrected**

1. **The item names the compare and misses the second bcrypt operation on the same handler.**
   `internal/api/auth.go:313` runs `bcrypt.GenerateFromPassword(newPassword, bcryptCost)` on the
   success path. A successful change therefore costs **two** cost-12 operations plus a transaction
   with two writes, not one compare.

2. **The item's repro is the CHEAPER of the two available attacks, which inverts its step 3.**
   `DeleteOtherTokensForUser` is `DELETE FROM api_tokens WHERE user_id = $1 AND id <> $2`
   (`internal/store/query/tokens.sql:44`), so a successful change spares the caller's own token. An
   authenticated caller who knows their own password can therefore loop *successful* changes - new
   password each time - forever, on one bearer token, at two cost-12 operations plus a transaction
   per iteration. That loop never produces a failure. Consequences: the flat ceiling this spec
   chooses bounds both loops, and any control conditioned on recent *failures* (the item's step 3)
   bounds only the cheap one. See section 6.

3. **"A job submission's cost is dominated by database inserts the server can batch" does not
   describe this tree.** `CreateJobFromSpec` does one sequential INSERT per task up to
   `maxTasksPerJob`, plus one per dependency edge; the predecessor spec says so in its own
   "Composition order" section and nothing batches them. The item's conclusion is unaffected - this
   route is CPU by construction and cannot be made cheaper without lowering the cost factor - but the
   clause as written is a claim about the tree that the tree does not support, and it should not be
   carried forward into the README row.

4. **"A bound near 1 per few seconds refuses nothing real" is wrong on the burst axis.** The
   sustained rate is right; the burst of 1 is not. A human who mistypes their current password gets
   a 403 and immediately retries, and a ceiling of 1 refuses that second attempt. The legitimate
   pattern on this route *is* a small burst, which is precisely where the predecessor spec's
   "shorter window beats larger count at the same sustained rate" rule stops applying. See section 5.

5. **Step 3's premise assumes a design this spec does not take.** "Rather than only refused after it
   runs" describes an in-handler charge, like `allowSearch`. A middleware wrap refuses before the
   handler function is entered at all, so the compare cannot run on a refused request and there is
   nothing for step 3 to improve. See section 6.

Minor: the item's `[[bug-2026-08-29-post-v1-jobs-is-not-rate-limited]]` link now resolves under
`docs/backlog/closed/`. Not a defect, noted so the implementer does not read a broken link as a
missing item.

## 3. The bucket key: the authenticated user, and this route has no owner-versus-caller split

**The key is `AuthUser.ID`, through the existing `userRateLimitKey`.** The predecessor spec's
argument for the user key over `RemoteAddr` transfers verbatim and is not re-argued here: before
authentication there is no principal and the address is the only identifier that exists; after it
there is one, resolved server-side from a token-hash lookup and never read off the wire; keyed on the
address, one office egress collapses a studio into one bucket while one user with two machines gets
two. That argument already lives in `UserRateLimit`'s doc comment.

**What is different here, and it is the item's claim 1: the route is self-scoped, verified.**
`handleChangePassword` takes every identifier from the context principal and nothing from the wire:

- `s.q.GetUser(ctx, authUser.ID)` (`auth.go:302`)
- `SetPasswordHash{ID: authUser.ID, ...}` (`auth.go:328`)
- `DeleteOtherTokensForUser{UserID: authUser.ID, ID: authUser.TokenID}` (`auth.go:336`)

The route pattern is a literal, `PUT /v1/users/me/password` - no `{id}` segment - and the request
body carries only `current_password` and `new_password`. So the principal charged for the work is
the principal the work is done to. That is not true of the predecessor's `retry` and `run-now`
routes, where an admin may spend their own bucket on another user's job, and `UserRateLimit`'s doc
comment says so. **This route is the clean case the comment's operational fallback exists for**, and
that is worth one clause when the comment is edited (section 9), not a new paragraph.

**A principal with no renderable id is refused with 401 before any bucket is touched.**
`userRateLimitKey` returns `(string, bool)` and `UserRateLimit` returns 401 on `!ok`, ahead of
`rl.allow`. Three things follow, all already true and all inherited free by this mount:

- No request is ever bucketed under `""` by omission. `rl.allow(userRateLimitKey(u))` does not
  compile, which is the whole reason for the two-value return.
- An unidentified caller creates no map key, so the key space stays bounded by the users table.
- `TestUserRateLimitKey`'s last row pins that the key is `u.ID` and not the adjacent same-typed
  `u.TokenID`. A per-token key would be a fresh full bucket per login, and `issueToken` mints a new
  token on every login without invalidating the old one.

Note a small, declared behaviour change on an unreachable path. `handleChangePassword` reads
`authUser, _ := UserFromCtx(...)` and discards the ok, so a request reaching it with no principal
would today call `GetUser` with a zero UUID and answer 500. Behind the wrap it answers 401. The path
is unreachable through the mux, where the route is inside `auth`. This is the same shape as the
search slice's declared change on `handleListJobs` and needs the same one-line note in the PR.

## 4. This bucket is SEPARATE from `RELAY_JOB_SUBMIT_RATE_LIMIT`. Confirmed, on four grounds

The item asserts this and gives one reason. Confirmed, and the reasons are stronger than the item's:

1. **The two ceilings are three orders of magnitude apart and neither value works for both.** The
   submit bucket ships at `120:10s` (`cmd/relay-server/main.go:202`), chosen against a measured
   33.8 to 36.7 submissions per second from a CLI for-loop. This route's legitimate rate is a
   handful of requests per *year* per user. Folded in, either the password route inherits 120 per
   10 s - which refuses nothing and leaves the item open - or the submit ceiling drops to
   password-change rates and breaks the workflow relay is built around.

2. **The quantities are different nouns.** The submit bucket bounds how much *task execution* one
   principal buys; this bounds how much *CPU spent in a key-derivation function* one principal
   buys. The predecessor spec states the rule its bucket follows - "it covers routes that let a
   non-admin buy EXECUTION, not writes in general" - and this route buys no execution at all.

3. **Sharing would trade the wrong direction, twice.** A password-change burst would refuse a job
   submission, and a spent submit budget would refuse a legitimate password change. `main.go`'s own
   comment on the search bucket already names the first of those as the worse outcome to trade away.

4. **The tree already made this decision once.** The `?q=` read bucket is a separate instance of the
   same mechanism for the same reason, with the argument written at
   `cmd/relay-server/main.go:207-219`. A third instance is the established shape here, not a new
   one.

**Cost of the decision, stated rather than hidden.** Three buckets means three unstoppable `gcLoop`
goroutines per process and three `map[string][]time.Time` key spaces. Sizing follows the predecessor
spec's arithmetic: a key is a 36-byte UUID string plus a slice header plus at most `limit`
`time.Time` values at 24 bytes. At `5:1m` that is roughly 160 bytes per user who has changed a
password in the last minute, which is not a number worth defending against.

## 5. The window and the default

**Env variable: `RELAY_PASSWORD_CHANGE_RATE_LIMIT`, format `N:duration`, default `5:1m`.** Parsed by
`api.ParseRateLimit` with `log.Fatalf` on error, following the three existing rate-limit variables
exactly.

### What a legitimate user does that the bound must not refuse

Read off the two shipped clients rather than imagined:

- **The SPA** (`web/src/profile/PasswordTab.tsx`). One PUT per click of Update password. The button
  is `disabled={change.isPending}`, so a double-click cannot double-submit. Three client-side guards
  - confirm mismatch, min 8 characters, max 72 bytes - each `return` before `change.mutate()`, so a
  malformed attempt never reaches the server at all. There is no retry-on-error anywhere in the
  mutation.
- **The CLI** (`internal/cli/passwd.go`). One PUT per `relay passwd` invocation, behind three
  interactive masked prompts and a local mismatch check. No retry loop.

So the only way a legitimate user issues more than one request in quick succession is by **mistyping
their current password and trying again**: the handler answers 403 and the user retypes. Retyping
three fields in the SPA, or answering three masked prompts on the CLI, is the pacing.

**`5:1m` covers five consecutive human attempts inside one minute.** Nobody reaches that by hand at
the pacing above, and a user who does is a minute away from trying again. **A ceiling of 1, or of 1
per few seconds as the item suggests, would refuse the second attempt of a routine mistype**, which
is a real refusal of a real user.

### Why the window is a minute and not ten seconds

The predecessor spec's rule - at equal sustained rate, prefer the shorter window, because it permits
a smaller burst and recovers faster - was written for a route whose legitimate pattern has no burst.
Here the legitimate pattern *is* a burst of a few, so the count must cover it and the window is what
is left to choose. A minute is chosen because it makes the refusal delay legible ("wait a minute")
and because it puts this variable in the same family as `RELAY_LOGIN_RATE_LIMIT` (`10:1m`) and
`RELAY_REGISTER_RATE_LIMIT` (`5:1m`), which bound the same thing: a human entering a credential into
a form, at a cost the server pays in bcrypt.

**This bound is deliberately tighter than login's `10:1m`, and the reason is that a refusal here
cannot lock anybody out.** A refused login is a user who cannot get in. A refused password change is
a user who already holds a valid session, whose session is untouched, and who waits. That asymmetry
is what buys the tighter number, and the README row should say it so nobody "harmonizes" the two.

### There is no off value, and that is deliberate

`ParseRateLimit` returns an error when the count is `<= 0` or the duration is `<= 0`
(`TestParseRateLimit` pins `0:1m` as an error row), and `main` is `log.Fatalf` on the error. So
`RELAY_PASSWORD_CHANGE_RATE_LIMIT=0:1m` does not disable the bucket; the server refuses to boot,
exactly as the three existing variables do. **The escape is a large number**, `100000:1s`, which
leaves the bound visible as a number in the environment and in README. Do not add an off token here,
and do not relax `ParseRateLimit`: an off spelling that exists for one of four rate-limit variables
is a worse surface than none. This restates the predecessor spec's refutation 1 and `main.go`'s
search-bucket comment, and is repeated only because the README row must not imply an off setting.

The Go-level disabled state - zero on either field - remains reachable and is what every existing
`&Server{}` in tests relies on. It is not reachable from the environment.

**Recommended value for a farm whose users are not all trusted: `3:1m`.** Cost, stated: a user who
mistypes three times waits up to a minute. That is the whole cost, because no workflow on this route
is scripted.

## 6. The item's step 3: OUT, and it would be a worse control if it replaced the wrap

The item asks whether the compare can be skipped for a caller who has already failed recently.
**Ruled out, with three reasons in order of weight.**

1. **The acceptance criterion is already satisfied by the wrap, exactly and not approximately.** The
   criterion says "a burst on this route is refused BEFORE the bcrypt compare runs, not after".
   `UserRateLimit` is middleware: on a refusal it writes `Retry-After`, writes the 429 body and
   returns **without calling `next.ServeHTTP`**. `bcrypt.CompareHashAndPassword` is inside
   `handleChangePassword`, which is `next`. So every request past the ceiling is refused before the
   compare, and before the `GetUser` round trip that feeds it. Say the scope precisely: the first
   `N` requests in a window *do* run the compare, by design - a ceiling is not a per-request skip -
   and the criterion is about the refused portion, which is what "a burst" names.

2. **A failure-conditioned control bounds only the cheaper of the two attacks.** From refutation 2:
   the expensive loop is *successful* changes, two cost-12 operations plus a transaction per
   iteration, driven by a caller who knows their own password and whose own token survives every
   iteration. That loop generates no failures, so a recent-failure gate never fires on it. A flat
   per-principal ceiling bounds both loops identically because it does not care about the outcome.

3. **It buys headroom this slice has no use for.** The only thing a failure gate buys over a flat
   ceiling is the ability to set the flat ceiling *higher* while still punishing failers. The
   ceiling chosen here is already at the level a legitimate user cannot reach, so there is no
   headroom to buy back, and the cost is a second piece of per-principal state whose behaviour
   depends on the outcome of the check it gates.

**So step 3 adds nothing beyond the wrap, and as a replacement for it would be strictly weaker.**
This is a decision, not a deferral: if a future item wants it, the reason has to be something other
than "refuse before the compare", because the wrap already does that.

## 7. Where the limiter is constructed, and how one-per-Server is pinned

**Constructed once in `Server.Handler`, as a local, beside the existing `userLimit`:**

```
passwordLimit := func(h http.Handler) http.Handler { return h }
if s.PasswordChangeLimitN > 0 && s.PasswordChangeLimitWin > 0 {
    passwordLimit = UserRateLimit(s.PasswordChangeLimitN, s.PasswordChangeLimitWin)
}
```

mounted as `auth(passwordLimit(http.HandlerFunc(s.handleChangePassword)))`.

**Middleware, not the in-handler form `allowSearch` uses, and the reason is specific.** The search
bucket had to be charged inside the handler because the counted set is a *subset* of requests decided
by `parseFilterQ`, and a middleware predicate re-deriving "does this carry a needle" would be a
second implementation of that decision that only has to disagree once. Here there is no predicate:
every request to this route runs the compare unless it fails one of two cheap guards. The counted
set and the expensive set are the same set already, so the middleware form has no seam to get wrong,
and it is the form that satisfies section 6's criterion structurally.

**The one-per-Server property.** `searchLimiterOnce` exists because the search limiter is built
lazily on the first q-carrying request, so nothing else orders its construction; a `sync.Once` is the
only thing that stops a second instance, and `TestSearchLimiter_IsConstructedOncePerServer` pins it by
identity. This limiter is built eagerly at `Handler()` time, so its one-per-Server property comes
from `Handler` being called once per server. Stated honestly:

- **Where it comes from.** `buildHTTPServer` is the only place an `api.Server` is constructed and it
  calls `Handler()` exactly once, in the `&http.Server{...}` literal it returns. `Handler`'s own doc
  comment already carries the contract, and this slice must extend it (section 9): today it says
  "each call allocates a fresh job-submit bucket", which becomes incomplete with a second bucket.
- **What pins it executably.** Not a `sync.Once` and not an identity assertion, but the ceiling test
  itself: at a limit of 1, two requests driven through the *same* `srv.Handler` must answer the
  handler's own code and then 429. A limiter built per request, or per route closure evaluated per
  request, gives each request a fresh map and both answer the handler. Test 1 in section 8 is
  therefore the guard for this property as well as for the wiring, and its comment must say so.
- **What is not pinned, and the rule that stands in for it.** Nothing stops a future caller invoking
  `Handler()` twice on one `Server` and leaking a goroutine plus splitting the budget.
  `server_test.go` does exactly that, once per test, which is harmless because those goroutines die
  with the process. The rule is in `Handler`'s doc comment and this slice must keep it true: **build
  every limiter at `Handler()` time, never inside a route closure and never inside a handler.**

This makes three unstoppable `gcLoop` goroutines per process. That is the accepted cost of the
mechanism and is already documented for the first two.

## 8. Testing

**Lane facts, verified in the tree.** `.github/workflows/go-ci.yml`'s `test` job runs
`go test -race ./...` with no tags, so the default lane runs in CI and every `//go:build integration`
file does not. `internal/api/api_test.go` and `testhelper_test.go` are both integration-tagged, so
the whole handler harness for that package is unavailable in the default lane.
`internal/api/ratelimit_test.go`, `internal/api/search_ratelimit_test.go`,
`internal/api/server_test.go`, `cmd/relay-server/counters_wiring_test.go` and
`cmd/relay-server/job_submit_ratelimit_wiring_test.go` are untagged and do run.

**The seam that puts almost everything in the lane CI runs.** `cmd/relay-server`'s `stubAdminDB`
makes `BearerAuth` resolve any bearer token to an admin with a valid, renderable user id and **no
Postgres** (`TestStubAdminDB_ResolvesAUserWithARenderableID` pins the renderable id). And
`handleChangePassword` refuses `{}` at `len(req.NewPassword) < 8` **before** `GetUser` and before
either bcrypt call, so an allowed request answers a determinate 400 with no pool. That gives two
distinguishable outcomes on one route with no container: **400 means the handler ran; 429 means it
did not.**

Note what the harness cannot do and why the tests below use `{}`. `stubAdminDB.QueryRow` answers
every statement with `stubAdminRow`, whose `Scan` arity-checks for `GetTokenWithUserRow`'s three
UUID destinations; `store.User` has one, so a request that reached `GetUser` would answer 500 rather
than reaching the compare. The 400 is the cleaner discriminator and is what the sibling wiring test
already uses.

### Default lane, `cmd/relay-server/password_ratelimit_wiring_test.go` (new, untagged, runs in CI)

The file reuses `stubAdminDB` and adds a `putAsUser` sibling to `postAsUser`, plus a
`passwordBucketServer(n, win)` builder that sets **only** the password pair on `httpServerDeps`.
Leaving every other limit field zero is load-bearing, not incidental: it is what makes a crossed
assignment in `buildHTTPServer` (`s.PasswordChangeLimitN = d.searchLimitN`) produce a zero limit, an
unarmed bucket and a RED test.

1. **`TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit`.** Limit 2, window 1
   minute. Two `PUT /v1/users/me/password` with body `{}` answer **400**; the third answers **429**.

   Mutations it kills, and what each looks like at the recorder:
   - the route left as bare `auth(...)`: the third answers 400;
   - a hard-coded count instead of the field, or a deleted or crossed
     `s.PasswordChangeLimitN` assignment: the third answers 400;
   - the limiter wrapped **outside** `auth` as `passwordLimit(auth(h))`: the limiter then runs
     before `BearerAuth` has put anything in the context, so `userRateLimitKey` fails closed and the
     **first** request answers 401 rather than 400. The first assertion is what fails, and the
     failure names the composition order rather than the ceiling;
   - a limiter constructed per request rather than once at `Handler()` time: the third answers 400,
     because each request would carry its own fresh map. This is section 7's property, and the
     test's comment must say it is guarded here.

   The `Retry-After` assertion on the 429 is a **presence check only** and its comment must say so:
   the header's VALUE is already pinned for this exact middleware by
   `TestUserRateLimit_RetryAfterNamesWhenTheWindowActuallyClears`, whose 55..61 band under a
   one-minute window is what kills the zero-duration mutation. This slice adds no rate-limit
   arithmetic, so a second band test would kill nothing. **Writing `NotEmpty` without saying that is
   how trap (b) happened; saying it is the repair.**

2. **`TestBuildHTTPServer_AHumansRetryRunIsNotRefused`.** Limit 5. Five consecutive `{}` requests all
   answer 400; the **sixth answers 429**. The sixth is not optional: five 400s under a limit of five
   are also what a limiter that does nothing produces, so without it the test is vacuous against the
   implementation it describes. This is the executable form of "a normal password change is
   unaffected" for the retry-run case, in the lane CI runs.

3. **`TestBuildHTTPServer_ThePasswordBucketIsSeparateFromTheSubmitBucket`**, two directions in one
   table. Build with `jobSubmitLimitN: 1, jobSubmitLimitWin: time.Minute` and
   `passwordChangeLimitN: 1, passwordChangeLimitWin: time.Minute`.
   - Direction A: `POST /v1/jobs {}` answers 400, a second answers **429 (the control: the submit
     bucket is provably full)**, then `PUT /v1/users/me/password {}` answers **400**.
   - Direction B: the mirror - spend the password budget, prove it full, then `POST /v1/jobs {}`
     answers 400.

   This makes section 4's decision executable rather than prose, in the default lane, which is
   strictly better than the predecessor slice managed for its own shared-bucket decision. The middle
   assertion in each direction is the control; without it the final 400 is also what a fixture whose
   limiter never ran produces.

4. **`TestBuildHTTPServer_AHalfConfiguredPasswordLimitLeavesTheBucketOff`**, two rows: count set with
   a zero window, and window set with a zero count. Three `{}` requests each, all 400. The
   **zero-count row is the discriminating one**: relaxing the enable guard's `&&` to `||` constructs
   a limiter with limit 0, and `rateLimiter.allow` takes its over-limit branch on an empty window and
   indexes `hits[0]`, so that row fails loudly. The zero-window row cannot discriminate against the
   same relaxation and is there to state the contract on the field the count row does not exercise.
   This exists because the retro measured `||` surviving on the submit pair, since the only
   off-state test set both fields to zero while the field doc promises "zero on EITHER".

5. **`TestMain_PassesThePasswordChangeLimitItParsed`**, a `go/ast` guard. This is the answer to trap
   (a) and it is bought knowingly; see the subsection below. It parses the `cmd/relay-server`
   package (not one named file, per
   `docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md`'s 2026-08-21
   constraint) and asserts, for each of `passwordChangeLimitN` and `passwordChangeLimitWin` in the
   single `buildHTTPServer(httpServerDeps{...})` composite literal:
   - the key is present and its value is a **plain `*ast.Ident`**, not a `BasicLit`;
   - that identifier reaches both `ParseRateLimit` and the string literal
     `"RELAY_PASSWORD_CHANGE_RATE_LIMIT"` through unconditional assignments in `main`'s body, and
     **reaches no other `RELAY_*_RATE_LIMIT` literal**;
   - it is assigned **exactly once** across `main`'s whole subtree.

   The four mutations it kills are the four the retro measured green on the submit pair: the literal
   set to `0`; the field fed another same-typed local (`loginN`, `registerN`, `searchN`); the field
   omitted; a later `pwN = 0` inside an `if`. The shape is lifted from `TestWatchdogIsStartedByMain`,
   which already pairs identifier reachability with an env-var string literal at a position, because
   slice 4 measured a same-typed transpose going green without it.

   **What it cannot see, in its own comment:** a value laundered through a helper call, a renamed
   `envOrDefault`, and any question of *fidelity* - `pwN / 2` passes. It proves the wiring was not
   deleted or crossed, and nothing more.

   It additionally asserts the default string literal is `"5:1m"`. That one is a doc-and-code
   consistency check, not a behavioural one, and its comment must say so: its subject is the README
   row, which states the default as a number an operator plans against.

#### Trap (a), and why an eleventh copy is the answer rather than a boundary statement

The retro measured that zeroing `main`'s `httpServerDeps` literal left the **entire `cmd/relay-server`
lane green**, so the submit bucket can be silently off in production. The predecessor slice responded
by *stating the boundary* in `buildHTTPServer`'s comment and pointing at the general item. Repeating
that here would repeat the trap: for a security control, "silently off in production" is the worst
available failure and a sentence does not stop it.

But `idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md` counts ten copies of this guard
family in `cmd/relay-server` and says plainly that pasting another is the failure mode. Both things
are true, so the slice must pay for the copy rather than pretend it is free. Three obligations:

- The guard is written as a **one-row table** in the shape that item prescribes (identifier
  reachability, plus assignment count per identifier across the subtree, plus the env-literal
  positional check), so a generalization lifts it without redesign.
- Its comment names that item and says **do not paste a twelfth; this row belongs in that item's
  table** - the same sentence `TestTrailingLogWindowIsWiredIntoTheHandler` carries.
- **The slice appends a progress note to that item and proposes raising it from `low` to `medium`.**
  The item's own Notes say "reconsider the priority the next time somebody adds a post-construction
  field in `cmd/relay-server`". This slice is that time. The note is required scope, not
  housekeeping, and the priority change is a proposal for the human, not a unilateral edit.

### Integration lane, `internal/api` (does NOT run in CI)

6. **`TestChangePassword_ANormalChangeSucceedsUnderTheBucket`**, in a new `//go:build integration`
   file. With the bucket armed at 5 on a real server with a real token: one
   `PUT /v1/users/me/password` with the correct current password answers **204**, the stored hash
   changes, and the caller's own token still authenticates.

   **The comment this test owes, per CLAUDE.md's rule that a guard behind a build tag must be able to
   run.** It cannot run in the default lane because it is the only assertion in the slice that
   reaches `SetPasswordHash` and the commit, which needs a pool and a transaction; `cmd/relay-server`
   reaches `handleChangePassword` but stops at its length guard because `stubAdminDB` cannot answer
   `GetUser`. What would have to exist for it to run in CI is a `services: postgres` lane covering
   `internal/api`, which `docs/superpowers/specs/2026-09-04-integration-guards-ci-coverage.md`
   section 4.1 excludes on cost and which
   `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md` tracks as instance 6. The
   comment must name both, and must state that **this test cannot measure the compare's cost at
   all**: `SetBcryptCostForTest()` sets `bcryptCost` to `bcrypt.MinCost`, so the seeded hash is
   MinCost and the compare in this lane is cheap by construction.

7. **`TestChangePassword_ABurstIsRefusedAndAnotherUserIsNot`**, same file. At a limit of 3, four
   requests as user A give three handler answers and one 429 with a `Retry-After`; user B's first
   request on the same server is not 429. Per-user isolation through the real auth chain, end to
   end.

**Lane honesty.** Tests 6 and 7 do not run in CI. That is the same structural gap the package already
carries and is not created by this slice, and it is the reason tests 1 to 5 were pushed into the
default lane wherever the property allowed.

### RED-first, and the mutation battery

Tests 1 to 5 are on symbols absent at HEAD, so their RED is non-compilation, which is a weak RED.
**The behavioural RED is test 7**: at HEAD, four `PUT /v1/users/me/password` in a row all reach the
handler. Run it against HEAD first and record it in the PR, so the slice has a real reproduction and
not only a compile error.

| Mutation | Killed by |
|---|---|
| Route left as bare `auth(...)` | 1, the third request answers 400 |
| Limiter wrapped outside `auth` | 1, the FIRST request answers 401 |
| Limiter constructed per request instead of at `Handler()` time | 1 |
| Hard-coded count instead of `s.PasswordChangeLimitN` | 1 |
| `s.PasswordChangeLimitN = d.searchLimitN` (crossed assignment) | 1, since the fixture leaves `searchLimitN` zero |
| Enable guard `&&` relaxed to `||` | 4, zero-count row, which panics on `hits[0]` |
| Route added to the submit bucket instead of its own | 3, both directions |
| `main`'s literal set to `0` | 5 |
| `main`'s literal fed `searchN` or `loginN` | 5 |
| A later `pwN = 0` inside an `if` in `main` | 5 |
| Key on `clientIP(r)` | Existing `TestUserRateLimit_TheSameUserFromTwoAddressesSharesOneBucket` and its mirror |
| `Retry-After` computed as zero | Existing `TestUserRateLimit_RetryAfterNamesWhenTheWindowActuallyClears` |

Run each with a control that should die, verify the mutation actually applied before recording a
survivor, and never revert a mutation with `git checkout --` - restore from a copy.

## 9. Prose this slice falsifies, and the README claim to make

**`README.md`, env table, a new row immediately after `RELAY_JOB_SEARCH_RATE_LIMIT` (line 292)**, so
the four rate-limit variables are adjacent. The row must carry, and nothing else:

- `RELAY_PASSWORD_CHANGE_RATE_LIMIT`, `5:1m`, format `N:duration`.
- **Per authenticated user, not per source address**, with the one-clause reason and a pointer to
  the submit row rather than a second copy of the argument.
- The route it covers, by name, and that it is the only one.
- **Why the bound is this small:** the handler runs a bcrypt compare at cost 12 on every request and
  a second bcrypt operation on success, so the work is CPU by construction and cannot be made
  cheaper without lowering the cost factor that protects stored passwords.
- **What it costs a legitimate user:** a user who mistypes their current password five times in a
  minute waits up to a minute before the sixth attempt. Their session is untouched and they are not
  locked out.
- **Why it is tighter than `RELAY_LOGIN_RATE_LIMIT`:** a refused login is a user who cannot get in; a
  refused password change is a user who already holds a valid session and waits.
- 429 with `Retry-After`, and the existing note that no first-party client surfaces the header.
- **Per replica and in memory**, matching the other three rows: two replicas give a caller twice the
  budget and a restart clears every bucket.
- **There is no off value**, and what to set instead (`100000:1s`).
- **It bounds repetition of the bcrypt work, not request volume** - the limiter runs inside the auth
  chain, so every attempt still costs one token lookup, and the control for request volume is the
  open listener-admission item.
- The `3:1m` recommendation for an untrusted farm, with its cost stated.
- **The aggregate, once the measurement exists:** the bound is per principal, so what an actor with
  M accounts buys is `M x N` compares per window. Give the arithmetic with the measured per-compare
  figure, and note that minting accounts through `POST /v1/auth/register` itself costs a cost-12
  `GenerateFromPassword` bounded at `RELAY_REGISTER_RATE_LIMIT` per source address - which is a real
  brake and a weak one, because that limiter keys per IPv6 `/128`.

**`README.md`, the Session section (line 1565 and the bullets at 1586-1588).** One sentence beside
the `PUT /v1/users/me/password` row naming the variable, so a reader of the API reference does not
have to find the env table to learn the route is bounded.

**`internal/api/ratelimit.go`, `UserRateLimit`'s doc comment.** It currently says the user is "also
the unit the bounded cost belongs to" on `POST /v1/jobs` and that this is "NOT true of **the other
two routes**". With a fourth mount that enumeration is stale and its arithmetic is wrong. **Delete
the enumeration and keep the reason**: on a self-scoped route the principal charged is the principal
the work is done to, and on a route where one principal may act on another's resource it is not, so
the argument that carries every mount is the operational one. Per this project's rule that rewriting
prose regenerates claims, re-verify the surviving sentences against the router while editing rather
than assuming the untouched half is still true.

**`internal/api/server.go`, `Handler`'s doc comment.** "Each call allocates a fresh job-submit bucket
and starts the gc goroutine that prunes it" becomes incomplete. Restate it as a property of the
function rather than a census of buckets: each call allocates a fresh bucket per armed user-keyed
limiter and starts an unstoppable gc goroutine for each.

**`cmd/relay-server/http_server.go`, `buildHTTPServer`'s doc comment.** Two edits. Its per-pair list
gains the password pair with its guard named. And its closing paragraph - "THAT LAST CLAIM STOPS AT
THIS FUNCTION'S OWN ASSIGNMENTS ... The named fields removed the transposition hazard at the
`api.New` boundary, not at the literal that feeds this one" - becomes false as a general statement
once test 5 exists. Scope it: it remains true of `jobSubmitLimit` and `searchLimit`, and the
password pair's literal is covered by `TestMain_PassesThePasswordChangeLimitItParsed` until the
general item subsumes it.

**No new comment carries** a census of which routes are wrapped, a count of anything elsewhere, a
uniqueness claim about other code, or any history. The route list lives at the wrap in
`Server.Handler` and in README.

## 10. The measurement this slice owes

**The implementation must measure the cost-12 bcrypt pair on the machine it runs on, and record the
number WITH ITS INPUT in the PR.** Not "roughly a quarter second": the cost factor, the password byte
length, the iteration count, the median and the p95, the CPU model and the Go version. Both
operations, because the success path pays both:

- `bcrypt.CompareHashAndPassword` against a hash generated at cost 12, with a wrong password (the
  item's repro), and
- `bcrypt.GenerateFromPassword` at cost 12.

A throwaway `go test -bench` in the implementation session is the right instrument. **Do not commit
it**: a permanent benchmark of a third-party library's cost factor is a change detector on somebody
else's code, and this repo has no benchmark lane to run it in.

**What the measurement can and cannot change, so it is not read as decorative.**

- **It cannot move the default.** `5:1m` is bounded above by human behaviour, not by a CPU budget:
  it is the number a mistyping user cannot reach. A faster or slower compare does not change how
  many times a person retypes a password in a minute.
- **It makes the README row honest.** The row states an aggregate - `M accounts x N per window x 2
  operations` - and that arithmetic is meaningless without a per-operation figure. This is the same
  obligation the `?q=` row already carries with "at the measured cost".
- **Escalation rule.** If the measured compare-plus-generate pair exceeds one second on the
  reference machine, the README row must spell the aggregate arithmetic explicitly and point at
  `3:1m` as the value for an untrusted farm. The default still stands. If the pair is under 50 ms,
  say so plainly rather than repeating "expensive", and the row's emphasis moves from CPU cost to
  the fact that the bound is free for every real user.

`scripts/qcost/main.go` is the precedent for a committed measurement harness and is deliberately
**not** followed here: it exists because a database cost depends on seeded row counts nobody can
reproduce by hand, and a bcrypt cost depends on one integer and one CPU.

## 11. Provenance of every number

**Read directly from the tree in this session** (file and identifier given, no execution):

- `bcryptCost = 12` - `internal/api/auth.go:23`.
- Two bcrypt operations on the success path - `auth.go:308` (compare) and `auth.go:313` (generate).
- The caller's own token survives a change - `internal/store/query/tokens.sql:44`, `AND id <> $2`.
- Handler validation order: `readJSON`, then `len(NewPassword) < 8`, then `GetUser`, then compare -
  `auth.go:291-308`.
- Route registered as bare `auth(...)` - `internal/api/server.go:173`.
- Existing defaults `10:1m` (login), `5:1m` (register), `120:10s` (submit), `120:10s` (search) -
  `cmd/relay-server/main.go:193-220` and README lines 288-292.
- `ParseRateLimit` refuses a zero count or a zero window - `internal/api/ratelimit.go:21-27`;
  `TestParseRateLimit` pins `0:1m` and `10:0s` as error rows.
- The 401-before-`allow` ordering, and the `(string, bool)` key - `internal/api/ratelimit.go:74-82,
  128-145`.
- SPA client behaviour: button `disabled={change.isPending}`, three guards that `return` before
  `mutate()`, no retry - `web/src/profile/PasswordTab.tsx:75-110, 175`.
- CLI client behaviour: one request per invocation behind three prompts, no retry -
  `internal/cli/passwd.go:26-56`.
- `stubAdminDB` fills three UUID destinations and one bool and arity-checks both -
  `cmd/relay-server/counters_wiring_test.go:73-102`.
- Lane tags: `internal/api/api_test.go` and `testhelper_test.go` integration-tagged;
  `ratelimit_test.go`, `search_ratelimit_test.go`, `server_test.go`,
  `job_submit_ratelimit_wiring_test.go`, `counters_wiring_test.go` untagged.

**Re-derived arithmetic** (no execution, stated so it can be checked):

- A successful change costs two cost-12 operations plus one transaction with two writes. From the
  two call sites above.
- Bucket memory at `5:1m`: a 36-byte UUID string plus a slice header plus at most 5 `time.Time`
  values at 24 bytes, so of order 160 bytes per recently-active user. The formula and the 24-byte
  figure are the predecessor spec's, applied to a different `limit`.
- The aggregate an actor with M accounts buys: `M x 5` compares per minute. Trivial, stated because
  the README row depends on it.

**Quoted from a document, NOT verified in this session:**

- "Roughly a quarter second of a CPU core per request" - the backlog item. **Unverified in both
  directions.** No shell was available. It is consistent with commonly published cost-12 figures,
  which is not evidence. Section 10 makes measuring it a precondition of merge, and no decision in
  this spec rests on it.
- 33.8 and 36.7 `relay submit` iterations per second -
  `docs/retros/2026-09-04-authenticated-route-rate-limiting.md`. Quoted only to establish that the
  submit bucket's `120:10s` was sized against a measurement; no decision here uses it.
- The `Retry-After` band 55..61 under a one-minute window - the assertion in
  `TestUserRateLimit_RetryAfterNamesWhenTheWindowActuallyClears`, read from the test file, not run.
- bcrypt's 72-byte input cap - `web/src/profile/PasswordTab.tsx`'s comment. A claim about the
  library, repeated here only to explain why the SPA has a guard that blocks a request.
- The count of ten existing copies of the wiring-guard family in `cmd/relay-server` - the backlog
  item's own running tally, not recounted here. **Do not carry this number into any comment**; it is
  a count of other code and belongs in the item.

**Judgements, with the reasoning in the section named:**

- `5:1m` - section 5. The count comes from the retype burst the two shipped clients can produce; the
  window comes from legibility and from the login and register family.
- `3:1m` as the untrusted-farm recommendation - section 5.
- The escalation rule in section 10.

## 12. What this slice does NOT cover

- **No fleet-wide ceiling.** The bucket is per principal. M accounts buy M budgets. With
  `RELAY_ALLOW_SELF_REGISTER=true` an actor mints accounts at `RELAY_REGISTER_RATE_LIMIT` per source
  address, keyed per IPv6 `/128`.
- **No budget over time.** A patient adversary changing their password at exactly the ceiling rate
  forever is not bounded. That is a durable-state quota and is not this.
- **Per replica.** The map is in memory and dies with the process.
- **The auth lookup is not bounded.** Every attempt, refused or not, costs one `GetTokenWithUser`
  round trip. See `docs/backlog/bug-2026-08-23-http-listener-has-no-admission-bounds.md`.
- **`POST /v1/users/password-reset` is not limited**, and it runs a cost-12 `GenerateFromPassword`
  per call (`auth.go:400`). It is admin-only, and the predecessor spec's rule stands: rate-limiting
  an admin remedy refuses the operator's action during the incident they are responding to, and an
  admin who wants to hurt this server has better tools. Named here so the omission is a decision.
- **`POST /v1/users` is not limited** either, for the same reason (`internal/api/users.go:601`).
- **`POST /v1/auth/login` and `POST /v1/auth/register` are unchanged**, including their pre-existing
  collapse behind a proxy, which the predecessor spec already proposed as a candidate item.
- **`bcryptCost` does not change.** Lowering it is the one thing that would make this route cheap,
  and it is the thing protecting stored passwords.
- **No refusal counter on `GET /v1/server/counters`.** Deliberate, and the argument is the
  predecessor spec's: the signal would be driven by the very caller it describes, and its obvious
  remedy (raise the limit) is in the driver's favour. If it is ever added, the forgeability must be
  stated where the number is READ, in the same commit.
- **No change to `RateLimit`, `UserRateLimit`'s signature, `ParseRateLimit`, `api.New`'s signature,
  or any handler.**

## 13. Backlog items this spec proposes, for the human to accept

None are filed by this spec.

1. **Raise `idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md` from `low` to `medium`, and
   append this slice's copy to its tally.** Its own Notes ask for exactly this reconsideration the
   next time a post-construction field is added in `cmd/relay-server`. This is that time, and the
   copy count is the argument.
2. **A `services: postgres` lane for `internal/api`, or an explicit wontfix.**
   `docs/superpowers/specs/2026-09-04-integration-guards-ci-coverage.md` excluded it on cost and
   `idea-2026-08-23-integration-only-guards-ci-never-runs.md` carries it as instance 6. This slice
   adds a second security-relevant assertion that only a human running the lane will ever execute,
   which is one more data point for that item rather than a new item.

## 14. Open questions for the human

1. **The default.** `5:1m` is a judgement from the two shipped clients' behaviour, not a
   measurement, and section 10 says a measurement cannot move it. Confirm the number, or fix a
   different one now.
2. **The wiring guard.** Section 8 buys an eleventh copy of a guard family that an open item says
   must not be pasted again, in exchange for killing a mutation that turns the control off in
   production. Confirm that trade, and confirm the proposed priority bump on that item.
3. **Whether `POST /v1/users/password-reset` should be in scope after all.** It is admin-only and
   runs one cost-12 generate per call. The spec rules it out on the predecessor's admin-remedy rule.
   That rule was written for cheap admin writes, and this one is not cheap.
