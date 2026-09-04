# Authenticated-route rate limiting: a per-user bucket on the routes that buy execution

Date: 2026-09-04
Status: proposed
Source item: `docs/backlog/bug-2026-08-29-post-v1-jobs-is-not-rate-limited.md`
Predecessor slice: `docs/superpowers/specs/2026-08-29-task-and-command-count-bounds.md` (half 1 of the
same parent item)

Two other open items depend on the single decision this spec makes and are answered explicitly below:

- `docs/backlog/feature-2026-09-03-server-side-bound-for-text-search.md` wants the same limiter on
  authenticated reads carrying `?q=`, in its own bucket. See "Reuse for the read bucket".
- `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md` has a half 2
  that pairs a per-owner schedule cap with a rate limit on `POST /v1/scheduled-jobs`. This spec owns
  that route's rate-limit decision and rules it OUT, with a reason. See "Which routes".

## Scope

One new rate-limit bucket, keyed on the authenticated user, wrapping the three routes that let one
principal buy task execution. It reuses `rateLimiter` and `ParseRateLimit` in
`internal/api/ratelimit.go` unchanged; it adds a key function, a middleware constructor, two
`api.Server` fields, one env variable, and three route wraps.

It does not change `RateLimit`, `ParseRateLimit`, `api.New`'s signature, or the login and register
wiring. It does not change the three count constants in `internal/jobspec/jobspec.go`. It does correct
two pieces of prose that this change makes false.

## What this spec refutes, in the source item and in the facts it was briefed with

**1. "A zero disables the bucket" is true of the Go API and false of the operator surface.**
`Server.Handler` guards both existing wraps with `if s.LoginLimitN > 0 && s.LoginLimitWin > 0` (and the
register equivalent), so a zero does disable the bucket for a Go caller. But `ParseRateLimit` returns
an error when the count is `<= 0` or the duration is `<= 0` - `TestParseRateLimit` pins `0:1m` as an
error case - and `main` calls `log.Fatalf` on that error for both variables. **So `RELAY_LOGIN_RATE_LIMIT=0:1m`
does not disable the login limiter; it refuses to boot.** The disabled state is reachable only from Go,
which is why `server_test.go` can build `&Server{}` with no limits. This matters for the new variable's
configuration shape and is settled under "Configuration".

**2. The item names two routes and the important one is neither of them.**
`POST /v1/jobs/{id}/retry?task=all` re-runs every task in an existing job. It carries **no request
body at all**, does no spec validation, creates no new rows, and is owner-or-admin gated by
`jobOwnerOr404`. On a job at the count bounds it re-buys 25,000 subprocess spawns and 25,000
`task_logs` rows per call. It is by a wide margin the cheapest repetition primitive in the HTTP API: a
zero-byte POST buys the same execution as a 1 MiB submission. A limit on `POST /v1/jobs` alone leaves
it untouched.

**3. `POST /v1/scheduled-jobs/{id}/run-now` is a second uncovered job-creation path.**
`handleRunScheduledJobNow` calls `CreateJobFromSpec` on the stored spec, as the schedule owner, inside
its own transaction. It is gated by `ownedScheduledJob` and is otherwise identical in cost to
`POST /v1/jobs`, with the body already stored so the caller does not even have to send it. Neither
backlog item mentions it.

**4. The boot-sweep item's half 2 pairs two controls that bound different things.** It asks for "a
per-owner schedule cap **and** a rate limit on `POST /v1/scheduled-jobs`". The hazard that item
describes is `ListEnabledScheduledJobs` materialising the whole enabled set at boot, which is
proportional to the **size** of `scheduled_jobs`. A rate limit bounds the rate of growth, not the
size, so it changes how long the table takes to become a boot hazard and does not bound the hazard.
The cap does. Ruling below.

**5. The `?q=` item's phrase "the existing per-IP `RateLimit` applied to authenticated reads" does not
survive contact with the composition order.** `RateLimit` keys on `clientIP(r)`, and both places it is
mounted are public routes with no `auth` in the chain. Applying the existing per-IP limiter to an
authenticated read would inherit exactly the proxy failure argued against below. What that item wants
is a second instance of the mechanism this spec builds, not a second mounting of `RateLimit`.

**6. The count-bounds spec's residual paragraph and the `maxCommandsPerJob` doc comment both become
false.** Both state that `POST /v1/jobs` carries no rate limit. The comment additionally spells a
census of what `server.go` wraps, which the project's comment rules forbid. Correcting both is in
scope; see "Advertisement surfaces".

## The key: the authenticated user

The bucket is keyed on `AuthUser.ID`, rendered through the existing `uuidStr` helper. Not the source
address, and not both.

### Why the `RemoteAddr` reasoning does not transfer

`RateLimit`'s key is right for `login` and `register` **because there is no principal yet**. The whole
job of the limiter there is to bound guessing by an actor who has proved nothing, and the only
identifier available before authentication is the transport's source address. Trusting
`X-Forwarded-For` would hand the guesser its own bucket key, so the limit would be self-defeating: one
header rotation per request buys an unbounded number of fresh buckets. `RemoteAddr` is chosen there
precisely because it is the one identifier the caller cannot choose.

On `POST /v1/jobs` the caller has already proved something. `BearerAuth` has resolved a token hash to a
row and put an `AuthUser` in the context. That principal is unforgeable in the same sense
`RemoteAddr` is - it is resolved server-side from a hash lookup, never read off the wire as a claim -
and unlike the address it is the unit the bounded cost actually belongs to. Task rows, subprocess
spawns, `task_logs` rows and dispatcher backlog are charged to a user's jobs. They are not charged to
a network path.

### What a shared source address does to unrelated callers

The address key fails on an authenticated route in both directions, and both failures are routine
rather than adversarial:

- **Collapse.** A studio is one office egress. A cloud deployment is one load balancer. Every artist
  presents the proxy's address, so one bucket holds the whole fleet. Sized for the fleet it bounds
  nobody; sized for a user it takes the studio down the first time one artist's script loops. There is
  no value that is both.
- **Escape.** One user with a workstation and a laptop, or on a VPN, or roaming between office wifi
  and a phone hotspot, gets a fresh full bucket per address. Under IPv6 the escape is not incidental:
  the smallest delegation anybody receives is a `/64` and every address in it is free to its holder,
  which is the reasoning already written into `RELAY_GRPC_MAX_CONNS_PER_IP`'s README row for the gRPC
  port.

These are the same fact seen from two ends: an address is neither necessary nor sufficient to identify
the principal that owns the cost.

### Not both

A conjunction (refuse when either bucket is full) is decided by its strictest arm, so adding the
address arm re-imports the collapse failure in full and buys nothing the user arm lacks, because every
request on these routes has an authenticated principal by construction. A disjunction (allow when
either has room) is worse than no limit: it advertises a bound that any caller escapes by moving one
of two keys. Neither is taken.

### Rejected: keying on the token instead, to avoid the database round trip

The token hash is available before `BearerAuth` runs, so a token-keyed limiter could sit outside the
auth chain and refuse without a lookup. It is rejected on two independent grounds.

`issueToken` mints a fresh 32-byte random token on every login with a 30-day expiry and does not
invalidate the previous one, so a caller who can log in accumulates distinct valid tokens at the login
rate. Each is a fresh full bucket under a token key, so the limit is evadable at the login limiter's
own rate by the same principal.

Worse, the key would be computed before the lookup that rejects an invalid token, so **any peer that
can reach the port mints unbounded map keys with garbage bearer headers**. That is a strictly worse
memory story than anything argued in "Memory" below, on a limiter that does not limit.

### The strongest argument against the user key, stated rather than hidden

**It does not bound an actor who controls several accounts.** With `RELAY_ALLOW_SELF_REGISTER=true`
an actor mints accounts through `POST /v1/auth/register` at `RELAY_REGISTER_RATE_LIMIT` (5 per minute
per source address by default), and every account is a fresh full bucket that persists. The aggregate
write rate available to a determined actor is therefore `accounts x limit` and grows over time. The
user key is a per-principal blast-radius and fairness control. **It is not a fleet ceiling, and this
slice ships no fleet ceiling.** An operator whose farm is not trusted end to end should leave
self-registration off, which is the default, and should read "What this slice does NOT cover".

## Composition order, and what it costs

`RateLimit` currently wraps the handler with no `auth` anywhere in the chain, because both routes it
wraps are public. A user-keyed limiter cannot sit there: nothing has resolved a principal yet. It must
run **inside** the auth chain:

```
CORS -> mux -> BearerAuth -> UserRateLimit -> handler
```

spelled at the route as `auth(userLimit(http.HandlerFunc(s.handleCreateJob)))`.

**The consequence is real and must be stated where the control is documented: every request pays one
`GetTokenWithUser` round trip before the limiter can refuse it.** A refused caller still costs one
pool connection checkout and one indexed lookup per attempt, so the limiter bounds the expensive work
and does not bound the auth lookup.

Whether that matters:

- **Quantitatively, no.** `GetTokenWithUser` is a single indexed lookup on a token hash. What it gates
  on `POST /v1/jobs` is a transaction with one sequential INSERT per task up to `maxTasksPerJob`, one
  more per dependency edge, a NOTIFY, and a dispatcher wake. The ratio is orders of magnitude, so the
  limiter captures effectively all of the cost it exists to bound.
- **Qualitatively, it is a real residual, and it is not new.** An actor holding one valid token can
  drive `GetTokenWithUser` at network rate today on any of the forty-odd authenticated routes. This
  change neither creates that exposure nor widens it, and it cannot close it: by construction you
  cannot know the user before the lookup that identifies them. The control for it is a request or
  connection admission bound at the HTTP listener, which is the open item
  `docs/backlog/bug-2026-08-23-http-listener-has-no-admission-bounds.md`. This spec does not
  substitute for it, and the README row must say so rather than let an operator read this limit as a
  bound on request volume.

**Rule for a future wrap, stated here because it is easy to get backwards.** The limiter goes
immediately inside `auth` and **outside** `admin`: `auth(userLimit(admin(h)))`. Placing it inside
`admin` makes a non-admin's rejected probes free; placing it outside charges them to the prober's own
bucket, which is what bounds probing. No route in this slice is admin-gated, so nothing depends on it
today.

## Burst ceiling, not a budget over time

`rateLimiter` is a sliding window of N hits per window. This slice uses it as a **burst ceiling**: a
short window and a count that no human or scripted workflow reaches.

The harm being bounded is instantaneous. A runaway loop, a buggy CI job or a client retrying on the
wrong error fills the dispatcher backlog and `task_logs` in seconds, and that is the realistic
incident on a solo-operator farm. A ceiling stops it inside its own window and clears itself, so the
refusal is a delay of seconds rather than a rejection.

**What a budget over time would have bought, and what it costs.** A per-user quota (N submissions per
day, refilling) is the only thing that bounds the patient adversary: the one who submits at exactly
the ceiling rate forever. Nothing in this slice bounds that actor. Buying it needs durable state
surviving restart (a Postgres counter, not an in-memory map), a quota-exceeded surface an operator can
inspect and override, and a policy decision about what happens to an artist's legitimate 200th
submission on a busy day. That is a quota feature with its own spec, and folding it in would make this
slice about two things. It is named in "What this slice does NOT cover" so the omission is a decision
rather than a gap.

**Shorter window beats larger count at the same sustained rate.** `60:10s` and `360:1m` permit the
same 6 per second sustained, but the shorter window permits a smaller burst and recovers six times
faster. Both axes favour the short window, so the default uses one.

## Which routes

### In: one shared bucket over three routes

| Route | Handler | Why |
|---|---|---|
| `POST /v1/jobs` | `handleCreateJob` | The item's subject. Creates a job and its whole task graph. |
| `POST /v1/jobs/{id}/retry` | `handleRetryJob` | Re-runs an existing job's tasks with a zero-byte request. Refutation 2. |
| `POST /v1/scheduled-jobs/{id}/run-now` | `handleRunScheduledJobNow` | Creates a job from a stored spec through `CreateJobFromSpec`. Refutation 3. |

**One bucket, not three.** The quantity being bounded is how much execution one principal can buy per
unit time, and it does not care which verb bought it. Three buckets multiply the ceiling by three and
let a caller alternate between them to stay under all of them. The shared bucket is also the reason
`retry` being the cheapest member is fine rather than a hole: it draws on the same budget as the
expensive members.

**The cost of including `retry`: a refused remedy.** A user whose budget is spent because a script
looped cannot retry a real failed job until the window clears. At a ten-second window that is a
ten-second delay with a `Retry-After` on it, which is acceptable. At a one-minute window set by an
operator it is a one-minute delay, which the README row must say.

**No admin exemption.** An admin's runaway script buys the same execution as anyone else's, and an
exempt class of caller is a control that does not exist for the people most able to trigger it. The
mitigation for the operational hazard (an admin refused during an incident) is the short window and a
default far above human rates, not an exemption.

### Out: `POST /v1/scheduled-jobs`

**Verdict: out.** Reasons, in order of weight:

1. **It is a bound on the wrong axis for the hazard that asked for it.** The boot-sweep item's hazard
   is the size of `scheduled_jobs` at boot. A creation-rate limit changes how fast the table fills and
   does not bound how full it gets. Shipping it would let that item record "bounded" for something
   that is not bounded, which is the shape this project treats as a defect in its own right.
2. **It bounds nothing about the execution a schedule buys.** A single schedule at `@every 1s` is a
   permanent, uncapped job-creation engine, and `schedrunner.fireOne` runs on the runner goroutine and
   never touches an HTTP route. **No HTTP rate limit anywhere can bound a schedule's firing.** So the
   creation route is not a smaller version of `POST /v1/jobs`; it is a different problem.
3. The control that does bound the table is a per-owner schedule count cap, which is a quota, is that
   item's other half, and is not this slice.

**Reversibility, so the decision is cheap rather than load-bearing.** If a future item wants this
route in the bucket, the change is one wrap in `Server.Handler` and nothing else, because the
mechanism is route-agnostic. It is not being ruled impossible; it is being ruled off-axis.

### What the boot-sweep item should record

Proposed edit to `docs/backlog/bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener.md`,
for the human to accept:

- Ship half 1 (keyset-page the sweep) alone. It stands unchanged and is unaffected by this spec.
- Amend half 2 to drop the rate limit and keep the cap. The rate limit is **decided, not deferred**:
  ruled out by `docs/superpowers/specs/2026-09-04-authenticated-route-rate-limiting.md` on the grounds
  above, and re-adding it is one line if a reason appears. Cite that path so the decision is findable
  and does not get re-litigated as an oversight.
- Retitle the remaining half so it names the cap rather than the pair, since an item whose title names
  two controls will keep being read as blocked on both.
- Its acceptance criterion "a decision is recorded on the quota and rate-limit half, even if it is
  'not now'" is satisfied for the rate-limit half by this spec.

### Out: everything else, and the rule

`POST /v1/reservations`, `POST /v1/invites`, `POST /v1/agent-enrollments`, `POST /v1/users`,
`POST /v1/users/password-reset` and the worker admin writes are all admin-only and all cheap per call.
Rate-limiting them refuses an operator's remedy actions during exactly the incident the operator is
responding to, and buys nothing: an admin who wants to hurt this server has better tools.

**The rule the bucket follows: it covers routes that let a non-admin buy EXECUTION, not writes in
general.** A future route that dispatches work belongs in it. A future route that writes a row does
not, unless someone argues the row's cost separately.

The gRPC agent port is out of scope entirely; it has its own admission controls
(`RELAY_GRPC_MAX_CONNS` and siblings) and its own budget (`ingest_log_budget`).

## Memory

`rateLimiter` holds `map[string][]time.Time`, prunes a key's expired hits on every `allow` call, and
runs `gcOnce` every five minutes to delete keys whose hits have all aged out. A key therefore costs
memory only for one window past its last request, and at most `limit` timestamps.

**Which key space is the safer unbounded map: the user, decisively.**

- **Under the user key, the space is the users table, and an ordinary caller cannot mint keys.** A key
  appears only when a principal actually issues one of the three requests. New principals come from
  `handleRegister`, which requires an invite token unless `RELAY_ALLOW_SELF_REGISTER` is on and is
  rate-limited at `RELAY_REGISTER_RATE_LIMIT` per source address either way, or from
  `handleAdminCreateUser`, which is admin-only. So the answer to "can an attacker mint keys" is: not
  without minting accounts, and not at all on a default deployment.
- **Under an address key the map is attacker-mintable with one valid token.** Even mounted inside the
  auth chain, where only authenticated requests reach it, a single principal presents from as many
  addresses as it holds, and an IPv6 `/64` is effectively unlimited. One valid token would buy an
  unbounded key space.

**The new bucket needs no bound the existing one lacks, and adding one would be a mistake.** A capped
map has to decide what happens at capacity and both answers are bad: fail open means an actor who
fills the map switches the limiter off for everyone, and fail closed means they refuse everyone. The
256-key cap on `watchdog.counts.swept_by_worker` is the right shape for a map that is **serialized
into a response** on every request, where the cost is per response and unbounded key cardinality is a
payload problem. This map is never serialized, is self-pruning, and its cost is per window. Do not
copy that cap here.

Sizing, so the claim is a number rather than a shrug: a key is a 36-byte UUID string plus a slice
header plus up to `limit` `time.Time` values at 24 bytes each. At the default `60:10s` that is roughly
1.5 KB per actively-submitting user, so 10,000 simultaneously-submitting users is on the order of
15 MB. On a farm with that many active submitters, memory in this map is not the binding constraint.

One operational note: `RateLimit` starts a `gcLoop` goroutine per call and never stops it. The
constructor must be called once, at `Server.Handler` build time, as the existing two are. `Handler`
is called once per server by `buildHTTPServer`; `server_test.go` calls it per test, which is fine
because those goroutines die with the process.

## Reuse for the read bucket

The `?q=` item wants the same treatment on authenticated reads carrying a needle, in its own bucket,
so unfiltered list polling is unaffected. **The mechanism reuses, with one design constraint this spec
settles now because getting it wrong is a security bug.**

**What reuses verbatim:** `rateLimiter` (its own instance, its own `limit`, `window` and map, so the
buckets are genuinely separate), `userRateLimitKey`, the 429 body and the `Retry-After` computation,
and `ParseRateLimit` for a second `N:duration` variable. Nothing in the write bucket's design is
specific to writes. The read item does not need to refactor anything this slice ships.

**What must not be copied: the middleware form.** A middleware would have to decide "does this request
carry a needle" by re-parsing the query string, and `parseFilterQ`'s own doc comment records why that
is unsafe: `r.URL.Query()` discards percent-decoding errors, so a second parse can disagree with the
one that was validated, and `qs.Get("q")` returns only the first value of a repeated parameter. A
middleware predicate written independently of `parseFilterQ` is a second implementation of the same
decision, and the two only have to disagree once for the expensive path to become unbudgeted.

**The shape the read item should take instead:** call `rl.allow(userRateLimitKey(u))` **inside the
handler**, at the point where `parseFilterQ` has already returned a non-nil needle. Then the counted
set and the expensive set are the same set by construction, using the same value, and there is no
second parse to disagree. This is why `rateLimiter` and its `allow` method stay unexported and
in-package rather than being hidden behind the middleware closure: the read bucket needs the value,
not only the wrapper.

Concretely, this slice must leave behind: `rateLimiter` with its existing `allow(key string)` method
untouched, and `userRateLimitKey(u AuthUser) string` as a package-level function rather than an
inlined expression inside the middleware. That is the whole cost of the reuse, and it is zero new
machinery.

The statement-timeout half of the `?q=` item is independent of all of this and is unaffected.

## Configuration

**Env variable: `RELAY_JOB_SUBMIT_RATE_LIMIT`, format `N:duration`, default `60:10s`.** Parsed by
`api.ParseRateLimit`, following `RELAY_LOGIN_RATE_LIMIT` and `RELAY_REGISTER_RATE_LIMIT` exactly,
including `log.Fatalf` on a malformed value.

**Why `60:10s`.** Six submissions per second sustained, sixty in a burst. Interactive use is nowhere
near it. A refused caller recovers within ten seconds and gets a `Retry-After` saying so. A client
looping at machine rate is refused within ten seconds of starting, having bought at most sixty
submissions.

**The number is a judgement, not a measurement, and the implementation must turn it into one.** The
shape that decides it is a CLI for-loop (`for f in *.blend; relay submit ...`), which spawns a process
per submission and is therefore paced by process start rather than by the network. Nobody has measured
that rate on this project. **The implementation must time a 200-iteration `relay submit` loop against a
local server and record the number and the machine in the PR.** If the measured rate exceeds six per
second, the default is raised to `120:10s` before merge rather than after a support ticket. That is a
concrete step, not a caveat.

**What zero means: it is not a legal value.** `ParseRateLimit` refuses a count of zero, so
`RELAY_JOB_SUBMIT_RATE_LIMIT=0:10s` refuses to boot, exactly as the two existing variables do
(refutation 1). This is deliberate consistency rather than an oversight, and the README row must not
imply an "off" setting exists. An operator who wants the bucket effectively disabled sets a large
count, and the README row should say which spelling to use. **Do not add an off token to this variable
alone**, and do not relax `ParseRateLimit`: `TestParseRateLimit` pins the refusal, and an off spelling
that exists for one of three rate-limit variables is a worse surface than none.

**Recommended value for a farm whose users are not all trusted:** `10:1m`. Say what it costs in the
README row: a 200-job for-loop is refused partway and the workflow becomes one job with 200 tasks,
which is the model relay is built around anyway.

### Threading it without making the transposition hazard worse

`buildHTTPServer`'s own doc comment names the live hazard: `api.New` is positional and takes four
same-typed arguments in a row, so swapping the login pair with the register pair compiles and leaves
every package green. **Adding a fifth and sixth positional argument would make that worse and is not
done.**

Instead the new bucket is set the way `Metrics`, `StaticHandler` and `AllowSelfRegister` already are:
as named fields on the `*api.Server` that `buildHTTPServer` holds.

- `api.Server` gains `JobSubmitLimitN int` and `JobSubmitLimitWin time.Duration`, named to match the
  existing `LoginLimitN` / `LoginLimitWin` pair.
- `httpServerDeps` gains `jobSubmitLimitN` and `jobSubmitLimitWin`, so `main`'s call site names them.
- `buildHTTPServer` assigns `s.JobSubmitLimitN = d.jobSubmitLimitN` and the window likewise.
- `api.New`'s signature does not change, which also avoids touching the dozens of
  `api.New(pool, q, ..., 0, 0, 0, 0)` call sites in the integration-tagged tests. Those call sites
  keep compiling and the new bucket simply defaults to off in them, which is what they want.

The two named-field assignments cannot be transposed with each other: one is an `int` and one is a
`time.Duration`, so a swap does not compile.

**This trades a transposition hazard for the deletion hazard the same comment names** ("deleting any
of the three assignments below is likewise green everywhere"). That is addressed by the default-lane
wiring test below, which drives a real request through `buildHTTPServer`'s output with a known limit
and would go RED on a deleted or substituted assignment. It is not addressed by
`countersAssignmentSources`, whose walk is specific to `s.Counters` assignments and does not
generalize; do not claim it covers this.

## The doc comment the acceptance criterion requires

The item requires the keying argument to live in a doc comment in the shipped code. It goes on the new
`UserRateLimit` constructor in `internal/api/ratelimit.go`, directly beside `RateLimit`, so a reader of
either keying argument sees the other.

It must carry, in a few lines and no more:

- The key is the authenticated user `BearerAuth` resolved, not the source address.
- Why `RemoteAddr` is right for the public routes and does not transfer: before authentication there
  is no principal and the address is the one identifier the caller cannot choose; after it there is a
  principal that is unforgeable in the same way and is the unit the bounded cost belongs to.
- What the address key would do here: one office egress or load balancer collapses a studio into one
  bucket, and one user with two machines gets two.
- That this must run inside the auth chain, so a refused request has already paid one token lookup.
- That a request with no `AuthUser` in context is refused rather than passed through.

Per the project's comment rules it must **not** contain: a census of which routes are wrapped, a count
of anything elsewhere, a uniqueness claim about other code, or any history. The route list lives in
`Server.Handler` where the wraps are, and in README.

## Testing

Lane facts, verified rather than assumed. `.github/workflows/go-ci.yml`'s `test` job runs
`go test -race ./... -timeout 180s` with **no tags**, so the default lane runs in CI and every
`//go:build integration` file does not. Its `Integration-tagged build check` step runs
`make vet-integration`, which type-checks the tagged code without running it. `internal/api`'s
`api_test.go` and `testhelper_test.go` are both integration-tagged, so the package's handler-level
harness is unavailable in the default lane. `internal/api/ratelimit_test.go` and
`internal/api/server_test.go` are untagged and do run.

There is a second, better default-lane seam that this spec depends on and that neither backlog item
knows about: `cmd/relay-server/counters_wiring_test.go` is untagged and defines `stubAdminDB`, a
`store.DBTX` that makes `BearerAuth` resolve any bearer token to an admin **with no Postgres**. That
is what makes an executable end-to-end wiring proof possible in the lane CI runs.

### Default lane, `internal/api/ratelimit_test.go` (runs in CI)

The middleware is exercised directly, with `ctxWithUser` supplying the principal.

1. **`TestUserRateLimit_TheSameUserFromTwoAddressesSharesOneBucket`.** Limit 1. Two requests with the
   same `AuthUser.ID` and **different** `RemoteAddr`; the second is 429. This is the headline
   discriminator: it is the executable form of the doc comment's claim and it is RED against the
   single most likely wrong implementation, which is reusing `clientIP`.
2. **`TestUserRateLimit_TwoUsersFromOneAddressDoNotShareABucket`.** Limit 1. Two requests with the
   same `RemoteAddr` and different `AuthUser.ID`; both 200. The mirror property, and the one an
   operator feels: a studio behind one egress is not collapsed. **Order the shared-address pair before
   any other assertion in the file's new cases**, so an early-exit mutation cannot pass by never
   reaching it.
3. **`TestUserRateLimit_ASustainableRateIsNotRefused`.** `UserRateLimit(2, 50*time.Millisecond)`, six
   requests for one user spaced 30 ms apart, all 200. This is the "a normal submission rate is not
   refused" half of the acceptance criterion, in the only non-vacuous form: two requests under a limit
   of three would be green against a limiter that does nothing, while this one requires the window to
   actually slide.
4. **`TestUserRateLimit_ARequestWithNoAuthUserIsRefused`.** A request whose context carries no
   `AuthUser` gets 401, not a pass-through and not an empty-string bucket. The middleware is only ever
   mounted inside `auth`, so a missing principal is a wiring fault; a shared `""` bucket would pool
   every such request together and a pass-through would be a silent hole. 401 matches what
   `handleCreateJob` already writes on its own `!ok` branch. Same test covers an `AuthUser` whose `ID`
   is not `Valid`, which `uuidStr` renders as `""`.

These four do not re-test the sliding window arithmetic. `TestRateLimit_WindowSlides`,
`TestRateLimit_OverLimitReturns429WithRetryAfter` and `TestRateLimit_ConcurrentHitsDontRace` already
pin `rateLimiter`, and this slice adds no new arithmetic to it. Duplicating them would add coverage of
nothing.

### Default lane, `cmd/relay-server` (runs in CI) - the wiring proof

5. **`TestBuildHTTPServer_TheJobSubmitBucketIsWiredWithTheConfiguredLimit`**, in a new untagged file
   beside `counters_wiring_test.go`. Build with
   `buildHTTPServer(httpServerDeps{q: store.New(stubAdminDB{}), jobSubmitLimitN: 2, jobSubmitLimitWin: time.Minute})`
   and issue three `POST /v1/jobs` with `Authorization: Bearer any` and the body `{}`. The first two
   reach `handleCreateJob` and answer **400** (`readJSON` succeeds, `ValidateJobSpec` refuses a missing
   name) **without touching the pool**; the third answers **429** with a `Retry-After`.

   This is the strongest available guard and it is worth naming what it covers that a source scan does
   not: the route is wrapped, the composition order puts the limiter after `BearerAuth`, the limiter
   uses the value `buildHTTPServer` was **given** rather than a freshly constructed one, and a deleted
   or substituted assignment is RED. It also pins that `handleCreateJob` reaches its 400 with no
   database, since `stubAdminDB` panics on any statement other than the bearer-auth lookup.

   **One prerequisite change to a shared helper.** `stubAdminRow.Scan` currently fills only `*bool` and
   `*string` destinations, so `GetTokenWithUserRow`'s two `pgtype.UUID` fields stay invalid and
   `uuidStr` renders `""`, which case 4 refuses. Add a `case *pgtype.UUID:` arm filling two distinct
   fixed valid UUIDs, with an arity check on the UUID destination count mirroring the existing bool
   check, so a row-shape change is loud rather than silent. `pgtype` is already imported in that file.
   The existing counters tests are indifferent to the value, but this is a shared helper, so run the
   whole `cmd/relay-server` default lane after the change.

6. **`TestBuildHTTPServer_ScheduleCreationIsNotInTheSubmitBucket`**, same file, same construction with
   `jobSubmitLimitN: 1`. Two `POST /v1/scheduled-jobs` requests with body `{}`; neither is 429. This
   makes the OUT verdict executable rather than prose, and it is what stops a later "consistency" edit
   from quietly adding the route. Assert "not 429" rather than a specific code, because the second
   request's outcome depends on `handleCreateScheduledJob`'s own validation order and the property
   under test is only that the bucket did not refuse it.

### Integration lane, `internal/api` (does NOT run in CI)

7. **`TestSubmitRetryAndRunNowShareOneBucket`**, in a new `//go:build integration` file. With the limit
   at 3 on a real server with a real token: one `POST /v1/jobs`, one `POST /v1/jobs/{id}/retry?task=all`,
   one `POST /v1/scheduled-jobs/{id}/run-now`, each answering its own success code, then a fourth
   request of any of the three answering 429.

   **This is the most valuable test in the set and it can only live here**, because reaching a success
   code on three different handlers needs three real handlers and a database. Without it, an
   implementation that gives each route its own `UserRateLimit` instance is green everywhere else, and
   the shared-bucket decision is the one that stops a caller alternating between routes.

8. **`TestPostJobs_ABurstIsRefusedAndTheNextUserIsNot`**, same file. Four `POST /v1/jobs` as user A at
   a limit of 3 gives three 201s and one 429 with a `Retry-After`; user B's first `POST /v1/jobs` on
   the same server is 201. Per-user isolation through the real auth chain, end to end.

**Lane honesty.** Tests 7 and 8 do not run in CI. The shared-bucket decision and the end-to-end
per-user proof are guarded only in a lane a human must run locally (`make test-integration`, or
`make vet-integration` for compilation alone). That is the same structural gap
`scheduled_jobs_response_test.go` already names for this package, it is not created by this slice, and
it is the reason cases 1 to 6 were pushed into the default lane wherever the property allowed.

### RED-first, and the mutation proofs to leave behind

Cases 1 to 6 are on symbols absent at HEAD, so their RED at HEAD is non-compilation, which is a weak
RED. **The behavioural RED is case 8**: at HEAD, four `POST /v1/jobs` in a row all answer 201. Run it
against HEAD first and record that in the PR, so the slice has a real reproduction and not only a
compile error.

Mutations to run, each with the permanent test that kills it:

| Mutation | Killed by |
|---|---|
| Key on `clientIP(r)` instead of the user | 1 and 2 |
| Drop the `!ok` / invalid-UUID refusal, pass through | 4 |
| Drop the `!ok` refusal, key on `""` | 4 |
| Give each of the three routes its own `UserRateLimit` instance | 7 (integration lane only) |
| Add `POST /v1/scheduled-jobs` to the wrap | 6 |
| Delete `s.JobSubmitLimitN = d.jobSubmitLimitN` in `buildHTTPServer` | 5 |
| Construct the limiter with a hard-coded count instead of the field | 5 |
| Wrap outside `auth` instead of inside | 5 (the request 401s before the limiter, so the third is not 429) |

Run each mutation with a control that should die, and verify the mutation actually applied before
recording a survivor. Never revert a mutation with `git checkout --`; restore from a copy.

## Advertisement surfaces

**README env table**, immediately after the `RELAY_REGISTER_RATE_LIMIT` row so the three rate-limit
variables are adjacent. The row must carry: the format and default; that the key is the **authenticated
user, not the source address**, and one clause on why; the three routes by name; that `POST /v1/scheduled-jobs`
is deliberately not in it and that a schedule's own firing is not bounded by any HTTP limit; that it is
**per replica and in memory**, so two replicas give a caller twice the budget, matching the per-replica
note the counters section already makes; the 429 with `Retry-After`; that there is **no off value** and
what to set instead; the `10:1m` recommendation for an untrusted farm with its cost stated; and, plainly,
**that this bounds repetition of expensive work and not request volume** - the auth lookup still runs on
every attempt, and the control for request volume is the open listener-admission item.

**README `tasks[].retries` row** currently says: "**All of these bound ONE request and none of them
bounds repetition** - `POST /v1/jobs` carries no rate limit, so the totals above are per-request figures
an authenticated caller may repeat." This slice makes the second clause false. Rewrite it to name
`RELAY_JOB_SUBMIT_RATE_LIMIT`, keep the honest part (the count caps bound one request), and state the
product plainly: the per-request caps times the per-window count is what one principal can buy per
window, and that number is still large.

**README `?q=` cost paragraph** says "The server applies no rate limit and no statement timeout to this
today." That stays true for reads and becomes ambiguous the moment a rate-limit row exists that a
reader might take as global. Scope it to reads in the same pass, in a few words. Cheap, and this
project treats a wrong contract in prose as a defect.

**`internal/jobspec/jobspec.go`, `maxCommandsPerJob`'s doc comment** says "`POST /v1/jobs` carries no
rate limit - internal/api/server.go wraps only register and login in `RateLimit` - so every figure above
is per-request and an authenticated caller may repeat it at whatever rate the network allows." Both
halves must change. Rewrite it to name the env variable and drop the census of what `server.go` wraps,
which the comment rules forbid and which is exactly the sentence that went stale.

**The three count constants themselves do not change.** The count-bounds spec chose the generous set
(`maxTasksPerJob = 5000`, `maxCommandsPerTask = 500`, `maxCommandsPerJob = 25000` - values confirmed in
the tree) and its "Rejected alternatives" section conditioned the tighter set (2000 / 200 / 10,000) on
this item being closed **wontfix**. It is being implemented, so the premise holds and the generous set
stands. Re-reading them is a required step of this slice per the source item's acceptance criterion;
the outcome of the re-read is "unchanged", and the PR must say so rather than leave it unstated.
`internal/jobspec/count_bounds_test.go` spells the numbers as literals rather than as the constants,
deliberately, so nothing in this slice touches it either.

## What this slice does NOT cover

- **No fleet-wide ceiling.** The bucket is per principal. N accounts buy N budgets, and with
  self-registration on an actor can mint accounts at `RELAY_REGISTER_RATE_LIMIT` per source address.
- **No budget over time.** A patient adversary submitting at exactly the ceiling rate forever is not
  bounded. That is a durable-state quota feature and is not this.
- **Per replica.** The map is in memory and dies with the process, so a two-replica deployment gives
  each caller twice the budget, and a restart clears every bucket.
- **The auth lookup is not bounded.** Every attempt, refused or not, costs one `GetTokenWithUser` round
  trip. See `docs/backlog/bug-2026-08-23-http-listener-has-no-admission-bounds.md`.
- **`POST /v1/scheduled-jobs` is not limited**, and the size of `scheduled_jobs` remains unbounded. See
  the boot-sweep item's remaining half.
- **A schedule's own firing is not limited by anything here**, because `schedrunner.fireOne` never
  touches an HTTP route. One `@every 1s` schedule is an uncapped job engine.
- **The `?q=` read bucket is not built here**, nor is the statement timeout that item also wants.
- **No refusal counter on `GET /v1/server/counters`.** Deliberate. A signal fed by a refusal path
  invites the question the counters README already answers for `task_status_fence`: what does a peer
  who can move this signal gain, and is its documented remedy in their favour. Here the signal is
  driven by the very caller it describes and the obvious remedy (raise the limit) is in the driver's
  favour. If it is ever added, the forgeability must be stated where the number is READ, in the same
  commit. Candidate item, not this slice.
- **`X-Forwarded-For` is still not trusted anywhere**, and this slice introduces no proxy assumption.
  It also does not fix the pre-existing collapse of the login and register limiters behind a proxy;
  that is unchanged and is a separate candidate item.
- **No change to `RateLimit`, `ParseRateLimit`, `api.New`'s signature, or the login and register
  wiring.**

## Backlog items this spec proposes, for the human to accept

None are filed by this spec. Each is specific and high-confidence:

1. **The login and register limiters collapse behind a proxy.** Both key on `RemoteAddr`, so a
   deployment behind a load balancer runs both public limiters as one fleet-wide bucket. Today that is
   undocumented; at minimum the two README rows should say it, and the fix is a decision about a
   trusted-proxy configuration rather than a bug fix.
2. **The `> 0` guards in `Server.Handler` describe a state the environment cannot produce.**
   `ParseRateLimit` refuses a zero count and `main` fatals on the error, so no rate-limit variable has
   an off value. Either give them one deliberately or record that the guards exist for Go callers only.
3. **A refusal counter for the rate limiters on `GET /v1/server/counters`**, with the forgeability
   analysis above as its opening constraint.

## Open questions for the human

1. **The default value.** `60:10s` is a judgement pending the CLI for-loop measurement the
   implementation is required to take. If the measured rate exceeds six per second the default becomes
   `120:10s`. Confirm the escalation rule, or fix the number now.
2. **Whether `POST /v1/jobs/{id}/retry` belongs in the same bucket as submission.** The spec argues yes
   (one budget for execution bought, whatever the verb). The cost is that a user whose budget is spent
   cannot use the remedy path for one window. The alternative is a second, larger bucket for retry
   alone, which the spec rejects as a knob nobody will tune.
3. **Confirmation of the `POST /v1/scheduled-jobs` OUT verdict** and of the proposed edits to the
   boot-sweep item, since that item's acceptance criterion requires a recorded decision.
