---
date: 2026-08-21
topic: grpc-admission-bounds
slice: 2026-08-20-grpc-admission-bounds
branch: claude/pr-merge-session-961184
range: origin/main..HEAD (backend only; Go only; zero SQL, zero migration, zero proto, zero generated file, zero files under web/; green, not yet merged, no PR opened at the time of writing)
closes: bug-2026-08-15-grpc-connection-admission-is-unbounded
---

# Session Retro: 2026-08-21 - a guard built from a mutation kill inherits the mutation's shape, not the defect's

**TL;DR:** the agent gRPC port now has four admission controls. `grpc.MaxConcurrentStreams(1)`, a
`keepalive.EnforcementPolicy` that is a deliberate behavioural no-op at grpc-go's own 5m
`defaultKeepalivePolicyMinTime`, `MaxConnectionIdle` at 60s, and a new `internal/netlimit` bounding
`net.Listener` with a total cap and a per-source cap that refuses by accept-then-close and never by
returning an error. Plus a registration deadline on the first `stream.Recv()` in
`internal/worker/handler.go`, which was not in the item, not in the spec and not in the plan. Four env
knobs: `RELAY_GRPC_MAX_CONNS`, `RELAY_GRPC_MAX_CONNS_PER_IP`, `RELAY_GRPC_MAX_CONN_IDLE`,
`RELAY_GRPC_REGISTRATION_TIMEOUT`. Unit 511 -> 554 top-level; all nine tagged integration packages
green; `-race` green on the three changed packages.

**The finding of the iteration is about guards, and it is new. Every structural guard in this slice
was written from the ONE instance a mutation happened to produce, rather than from the shape of the
defect, and each was then evaded on the first attempt. Three rounds running, on three different
guards.**

---

## The headline: a guard written from a mutation kill inherits the mutation's shape

This project already records **"a uniqueness claim is a claim about the complement"** - "X is the
only ..." cannot be checked by opening X. What happened here is its executable sibling, and it is
worse in one specific way: **there the miss was in a claim a reader could doubt; here it was in a
check that passed.**

**Round A. `TestGRPCServerOptionsHasExactlyOneKeepaliveParams`.** Mutation row 14 of the matrix found
one way to double-apply `grpc.KeepaliveParams`, and a guard was written for exactly that: parse
`grpc_config.go`, find `grpcServerOptions`, require exactly one `grpc.KeepaliveParams` call inside
it. Two one-line escapes survived, both proved by running them:

- **Call-site escape.** `grpc.NewServer(append(grpcServerOptions(b), grpc.KeepaliveParams(...))...)`
  in `main.go`. The guard parsed one file; the wiring guard only needs `NewServer` to *mention*
  `grpcServerOptions`, which `append` still does. The 30s/10s liveness probe silently became
  grpc-go's 2h default.
- **Sibling-option escape.** A second `grpc.KeepaliveEnforcementPolicy`. grpc-go stores that one
  wholesale too, and it is **literally the regression `grpcKeepaliveMinTime`'s own comment names**:
  somebody adding a policy with `MinTime: 10 * time.Second` "because that is what the internet
  suggests". The guard for the option next door did not cover the option the comment warns about.

**Round B. The widened guard,
`TestGRPCServerOptionNamesAreUniqueAcrossThePackage`, evaded by an import alias.** The rule was
correctly restated on the shape ("every `grpc.<Option>` constructor appears at most once across the
package's non-test files"), and then checked by matching the package *identifier*: `pkg.Name ==
"grpc"`. A new file importing `ggrpc "google.golang.org/grpc"` and contributing
`ggrpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: 2 * time.Hour})` through
`append(grpcServerOptions(b), extraServerOptions()...)` left the whole package green while the
liveness probe became 2h. **The identical mutation without the alias was killed, which is the tell:
the guard worked for one spelling of the thing it guards.**

And the non-vacuity escape hatch was itself the recorded defect class. It was
`require.NotEmpty(t, counts)` with a failure message naming the import alias as the hazard it
detected. It could not detect it: `counts` is empty only when the package makes **zero**
`grpc.<Option>` calls, which cannot happen while `grpc_config.go` makes three.

> **A principle stated in an assertion message is not a check.** This is the same shape as the
> already-recorded "a principle in a comment is not a check", one layer more dangerous, because an
> assertion message reads like evidence that the check exists. The guard now resolves the import path
> rather than the identifier, fails loudly on a dot-import, and counts *importers* rather than
> option calls, which does go to zero if `grpcServerOptions` moves out of the package.

**Round C. `TestGRPCAdmissionIsWiredByMain`'s `netlimit.Config` check, evaded by the next line.** The
guard was hardened to read the literal's fields out of the `Wrap` call. It was then evaded by
assigning those fields on the following line, because the "assigned exactly once" walk matched only
`*ast.Ident` on the left-hand side and never `*ast.SelectorExpr`:

```go
cfg := netlimit.Config{MaxTotal: b.maxConns, MaxPerIP: b.maxConnsPerIP}
cfg.MaxTotal = 0
cfg.MaxPerIP = 0
netlimit.Wrap(lis, cfg)
```

**Two lenses found this independently with two different mutations.** With both caps at zero,
`Accept` returns every conn unwrapped and there is no admission control left at all - and the package
was green. The repair requires the config to be a composite literal at the call site, and the failure
message says so verbatim.

> **The generalizable rule: a guard built from a mutation kill inherits the MUTATION's shape, not the
> DEFECT's.** A mutation is one instance drawn from the space of edits that break a property; a guard
> written from it defends that instance. The cheap discipline is to write the guard from the
> *property*, then adversarially search for the shape - other files, other spellings, other AST node
> kinds on the same syntactic position - and to record the hit count, exactly as the uniqueness lesson
> already demands for claims. Three for three this slice, which is not a sample worth arguing with.

---

## The slice as first implemented was defeatable by the exact adversary it was built to stop, for the second consecutive iteration

The 2026-08-20 coordinator watchdog was the first. This is the second, and the mechanism is
different enough to be worth keeping apart.

An unauthenticated peer opens one stream and sends nothing. `Connect` blocks on the first
`stream.Recv()` before authentication with no deadline. Opening a stream **zeroes grpc-go's
`t.idle`**, and a zero `t.idle` reschedules rather than reaps, so `MaxConnectionIdle` never reaps it.
The keepalive liveness probe does not reach it either: any frame the peer reads re-stamps
`t.lastRead`. Reproduced holding at 55s against a 200ms idle window.

**The slice made this materially worse than it found it.** Before, such a peer was bounded only by
the process file-descriptor limit, which is a nuisance orders of magnitude out. With a 1024-slot cap
introduced by this very slice, it became a **cheap, permanent, fleet-wide denial**: a handful of
source prefixes fill the cap and every real agent is refused from then on.

**Four prose sites in the diff asserted this could not happen**, including a test comment claiming
idle reaping is what stops slots being held "forever". The fix is `worker.DefaultRegistrationTimeout`
and `Handler.recvRegistration`, bounding **only** the first `Recv` - the message loop's `Recv` must
stay unbounded forever, since a healthy agent holds one silent stream for hours between dispatches.

That distinction got its own guard for a reason worth recording. Replacing the message loop's
`stream.Recv()` with `h.recvRegistration(stream)` - one token, three lines below a comment saying not
to - compiled, left `go test ./internal/worker` entirely green, and would have cut every healthy
agent at 30s of stream silence, fleet-wide and permanently. It also breaks `err == io.EOF` matching
through the error wrap, and nothing objected to that either.
`TestHandler_MessageLoopRecvIsNotBoundedByTheRegistrationDeadline` is the check behind the paragraph
now, and it lives in the integration lane because `Connect` reaches the message loop only after
`authenticateAndRegister`, whose three credential paths all go to the store.

> **The pattern to watch, now at two consecutive instances: a slice that introduces a CEILING
> converts every pre-existing "bounded only by exhaustion" hold into a denial primitive.** The
> question to ask at spec time is not "what does the cap stop" but "what does the cap make newly
> worth doing". Neither the item, the spec, the plan nor `/code-review` asked it; a Phase 4 lens did.

---

## A bound stated per unit is only a bound if the unit is bounded, and the fix for the above created a second instance of that rule inside this slice

The item's own headline lesson, reproduced by the remedy for it, one layer down.

The registration deadline and the idle window **compose**. The deadline ends the **stream**, and
ending the last stream is precisely what re-stamps `t.idle` and arms the reaper, so a stream-opening
peer holds its connection slot for the **sum** of the two. Measured against a real `grpc.Server`, not
argued: 300ms + 400ms held a slot for ~0.7s
(`TestGRPCServer_RegistrationDeadlineAndIdleWindowCompose`).

At the defaults that is 90s per TCP handshake, which is **1024/90 = 11.4 new connections per second**
to hold the entire fleet cap. That is **cheaper** than the ~17/s the README quoted for the pure-idle
route, so the slice's own comparison pointed the wrong way, and the test that asserted the property
asserted on **one of the two terms** while its comment described the whole hold.

Worse, `RELAY_GRPC_REGISTRATION_TIMEOUT=2m` is **sanctioned by README's own row** for a slow fleet.
At the default idle window that composite is 180s, past grpc-go's 120s `connectionTimeout` for a peer
that says nothing at all - which makes **saying something cheaper than saying nothing** and inverts
both controls, with nothing red and nothing logged.

`resolveGRPCBounds` is now the one place that sees both values and warns when they cross 120s.
`TestDefaultGRPCMaxConnIdleIsTheAttackersDutyCycle` asserts on the sum.

> **When two bounds are configured independently, something has to own the composite.** Each parse
> function can only price its own knob, and each of these two is individually defensible at values
> whose sum is not. The tell that a composite exists at all: bound A's remedy hands its subject back
> to bound B rather than terminating it.

---

## The per-source cap was void against IPv6, and that mattered because the total cap is a denial ceiling this slice introduced

Keying on the exact address meant `/128` for IPv6. The smallest delegation anybody receives is a
`/64`, every address in it is bindable by its holder for free, and each one lands in its own bucket at
count 1. So `MaxPerIP` never fired: **`RefusedPerIP` stayed at 0 throughout**, and the operator
summary reported fleet growth rather than the single host responsible.

That is not "a weaker cap for IPv6". It is **no cap at all for IPv6** - and the per-IP cap is the
*only stated mitigation* for the denial ceiling this slice introduced by capping the total. The
spec's section 10 listed IPv6 aggregation as explicitly out of scope, citing consistency with
`api.clientIP`. That consistency argument was wrong for this control: a login rate limiter meters a
cost that is already bounded per request, while this bounds a held resource whose worst case is a
fleet-wide denial that persists as long as the attacker cares to hold it.

Now aggregated to a `/64`, with IPv4 left exact and the asymmetry argued at length in
`hostKey`'s comment and in README's row.

**Flag for a release note: this is a behaviour change, not only a posture change.** An IPv6 site
running more than `RELAY_GRPC_MAX_CONNS_PER_IP` agents out of one prefix will now be refused where it
previously was not, because the cap genuinely did not exist for IPv6 before. The NAT-hazard paragraph
in README covers it, and an upgrading operator will not read README.

What aggregation does **not** buy is stated in source and in README rather than glossed: it raises the
bar to "one host per /64 the attacker holds" and no further. At the defaults each `/64` buys 64 slots,
6.25% of 1024, so **16 distinct /64s fill the fleet cap**, and a `/56` or `/48` delegation escapes in
proportion to its size.

---

## The item was largely right, and every stage of the pipeline found errors in the one before it

Five stages, five sets of refutations, in order.

**The spec refuted four of the item's claims against grpc-go source**, three of which changed what
shipped:

1. There **is** a default `EnforcementPolicy` (`MinTime` 5m, `PermitWithoutStream` false), so the
   item's "a client may ping as often as it likes" is false and there is nothing to tighten.
2. The agent sends **no keepalive pings at all** (`Time = infinity`), so `MinTime` could not be
   "derived from the agent's own keepalive settings" as the item instructed. There is no cadence.
3. A **stream interceptor is the wrong seam**: it runs per stream, after the TCP connection, the
   preface and the transport goroutines exist, so it refuses nothing that has not already been paid
   for. The `net.Listener` is the correct seam and needs no grpc-go internals.
4. The **enrollment-token path is already bounded** - the upsert and the single-use consume share one
   transaction, so one token buys exactly one row. The item did not say this, and it narrows the
   unbounded-rows exposure to `RELAY_ALLOW_AUTO_ENROLL` specifically.

**The plan then refuted ten spec claims**, of which two were structural:

- The keepalive tests as specced **could not be written at all**. `WithKeepaliveParams` clamps the
  client ping interval to 10s and `internal.KeepaliveMinPingTime` is unimportable, so the "abusive
  pinger" client the spec described cannot exist. The policy ships with a constant-lockstep assertion
  and no behavioural test, and the residual is recorded rather than faked.
- The spec's claim that a second `grpc.KeepaliveParams` would "silently discard Time and Timeout" was
  right about the mechanism and the plan proved it runnable, which is what produced the guard whose
  three-round evasion is this retro's headline.
- The plan also found, unprompted, that wrapping the accepted conn **drops `TCP_USER_TIMEOUT` on
  Linux** (`SetTCPUserTimeout` type-asserts `*net.TCPConn` and returns nil silently). Filed.

**The engineer refuted two conductor instructions; the lenses refuted two more.** See below.

> Fifteen iterations of "verify a backlog item's technical claims against the code" now has a
> companion worth stating separately: **the verification chain is only worth its length if each stage
> treats the previous stage's output as an untrusted artifact.** Every stage here found errors in the
> one before it, including the stages that were themselves right about everything else.

---

## The conductor introduced four defects this iteration, a new high

Recorded as a lane that needs the same verify-before-dispatch discipline as the spec and plan lanes,
because the trend is now three consecutive slices with a defect in a conductor-authored brief and this
one is four defects in a single slice.

- **(a) A service-count guard that was vacuous by construction.** The conductor suggested asserting
  the gRPC server hosts exactly one service; it was implemented as an assertion on a server **the test
  builds and registers one service on**, structurally unable to see `main.go`. Proved vacuous by
  adding `reflection.Register(grpcSrv)` to `main.go` with the package still green. Replaced by a
  `main.go`-parsing check.
- **(b) A dictated README sentence that was false.** It claimed a revoked-but-connected agent "keeps
  receiving dispatches". `ClearWorkerAgentToken` sets `status = 'revoked'` and `dispatch.go:213` skips
  any worker that is not `online` or `stale`, so **new dispatches stop at once**. Both re-verify
  lenses found it independently, and README already said it correctly 370 lines away, so the file
  contradicted itself. The paragraph now states the true version: existing work and its log/status
  writes continue, new dispatches do not.
- **(c) A false premise used to justify a number.** "A legitimate agent sends `RegisterRequest`
  immediately after opening the stream" - `buildRegisterRequest` calls `ListInventory` in that gap.
  The number (30s) survived; the derivation did not, and the comment now names what is actually in the
  window and what would invalidate it.
- **(d) A prescribed DB-free unit test that could not work.** `Connect` reaches the message loop only
  after `authenticateAndRegister`, and all three credential paths hit the store. The test lives in the
  integration lane.

The engineer caught (c) and (d) before writing code; the lenses caught (a) and (b).

---

## Mutation testing was the instrument for nearly every real finding, and it failed in a new way

Every guard evasion above, the vacuous service-count guard, and the field-assignment escape were all
found by running a mutation and observing green. Nothing else in the pipeline found them:
`/code-review` at high effort returned **2** findings on this diff, and the six-agent fan-out then
found **4 HIGHs it missed**.

The new failure mode: **the engineer's first battery silently did not apply.** The patches were
written with LF endings against a CRLF tree, the edits did not take, and the run reported
`ok (cached)`. **The tell was uniformity** - the exact lesson recorded from the watchdog slice one day
earlier, and this time it was **recognized rather than re-learned**, which is the first time a
recorded mutation-harness lesson has paid off in the next slice.

Diff shape recorded alongside the `/code-review` result, per the standing instruction:

| | diff shape | `/code-review` (high) | fan-out |
|---|---|---|---|
| 2026-08-14 cursor-pager | behaviour-preserving refactor, no logic delta | 0 | 2 |
| 2026-08-20 reconcile | 8 behavioural lines under 45 lines of comment | 0 | 6 |
| 2026-08-20 watchdog | new migration, query, package file, wiring, send path | 3 | 4 |
| 2026-08-21 grpc-admission | new package, new config file, new wiring, one handler change, four env knobs, heavy prose | 2 | 4 HIGH + more |

Fourth data point. The covariate holds in sign but weakens: this was the largest new-logic diff of the
four and produced fewer `/code-review` findings than the watchdog. The reading that survives all four
is the standing one - **a clean or thin `/code-review` is a lead, not a verdict** - and the reason it
was thin here is visible in what the fan-out found: three of the four HIGHs were about **guards that
pass**, and a review tool reads code for defects, not tests for adequacy.

---

## Two acceptance criteria were closed by written decisions, and the prose needed an adversarial pass too

The item explicitly permitted both: the per-peer cap belonging in-process rather than at a proxy, and
auto-enroll row creation staying unbounded.

A lens then found that the auto-enroll README paragraph **reintroduced a framing a prior review had
already corrected in a different spec**. It described the cost as row growth and omitted that claiming
an **in-use** hostname takes the worker over - the larger of the two costs, and an open bug
(`bug-2026-08-12-auto-enroll-hostname-takeover`). The paragraph now leads with takeover and treats row
growth as the smaller, second problem.

> **When prose IS the deliverable, it gets the same adversarial pass as code.** The failure here is
> not that the paragraph was wrong in a clause; it is that it was *complete about the thing it chose
> to discuss* and silently narrower than the trust model it claimed to state. A correction recorded in
> one spec does not propagate to the next document that describes the same mechanism.

---

## What Was Built

- **`internal/netlimit`** (new, one file plus tests). `Wrap(inner, Config{MaxTotal, MaxPerIP})`
  returning a `*Listener`. Read the package doc for the two rules that carry it: **refusal is a close,
  never an error** (grpc-go's `Serve` treats a non-`Temporary` `Accept` error as fatal and closes the
  listener, so an admission control expressed as an error takes down the server it protects; a
  `Temporary` error is also wrong, because `Serve` retries those with a backoff that would rate-limit
  every honest peer queued behind the abusive one), and **the two known consequences of wrapping the
  conn** (`TCP_USER_TIMEOUT` on Linux, channelz socket metrics).
- **Both caps disabled returns the conn UNWRAPPED**, which is the branch that lets grpc-go's
  `conn.(*net.TCPConn)` assertion succeed for an operator who caps connections at a proxy instead.
  `cfg` is immutable after `Wrap`, so the branch cannot change under a live connection.
- **`conn.Close` releases the slot exactly once, after the underlying `Close` returns.** The
  `sync.Once` is load-bearing, not defensive: grpc-go double-closes on its most common failure path (a
  peer that opens TCP and hangs up before the preface). The comment explicitly argues that the
  decrement-last ordering is **not** an instance of CLAUDE.md's "end the generation before releasing
  the resource" - there is no generation and no staleness guard here, this is a capacity semaphore,
  and releasing last is simply the fail-closed order.
- **`hostKey`** - IPv4 exact, IPv6 aggregated to `/64`, v4-mapped v6 unmapped first so a dual-stack
  listener cannot give one host two buckets, non-IP addresses falling back to the host string. Never
  `host:port`, which would make the cap a no-op that still passes a naive test.
- **A nil-conn skip in `Accept` that continues the loop rather than returning.** Handing the nil back
  only moves the panic one frame, into `handleRawConn`'s unchecked `rawConn.SetDeadline`, from a
  goroutine grpc-go does not recover either. The typed-nil case is disclosed rather than handled.
- **`cmd/relay-server/grpc_config.go`** (new) - the option list, four env parsers following
  `parseWatchdogDuration`'s three-outcome shape, `resolveGRPCBounds`, `grpcBoundsLine`, and the
  refusal reporter.
- **`grpcServerOptions` returns exactly one `grpc.KeepaliveParams`**, built by `grpcKeepaliveParams`,
  because grpc-go stores that struct wholesale and a second option silently discards `Time` and
  `Timeout`.
- **`grpcKeepaliveMinTime = 5 * time.Minute`, shipped as a behavioural no-op.** It is not picked, it
  is the unique non-regressive value: smaller is a loosening of what grpc-go already enforces, larger
  refuses pings grpc-go accepts today from a principal that sends none. Its value is that a future
  loosening now shows up in a diff.
- **`parseConnLimit` warns on `1`.** An agent closes its old connection client-side while the server
  releases the slot only on observing the close, so a cap of 1 can refuse an agent its own reconnect.
  README said in bold not to set it and the parser accepted it in silence; one of the two had to give.
  The value is kept, because narrowing a cap is the operator's prerogative.
- **`parseRegistrationTimeout` has no disable arm, and that is the one deliberate divergence** from
  every other knob here. Every other bound can be switched off because a proxy can substitute for it;
  no proxy can enforce "send a `RegisterRequest` within N seconds", because that is an
  application-layer property of relay's own protocol.
- **`resolveGRPCBounds` is a function rather than inline code in `main`** for a reason that is not
  tidiness: a structural guard over `main.go` could see `netlimit.Wrap` being called but not a
  `grpcBnds = grpcBounds{}` inserted just above it, a disclosed blind spot that disabled the entire
  control while every package stayed green. Building the value where `main` cannot shadow it removes
  that by construction.
- **A refusal SUMMARY, at most one line per minute, and only when a counter moved.** A line per
  refusal would be a new unbounded attacker-driven log site inside the control that exists to bound
  attacker-driven log volume. The line carries counts and never addresses.
  `TestLimitListener_RefusalWritesNothingToTheLog` guards the `Accept` side, because the reporter test
  alone is green under that mutation.
- **The registration deadline is deliberately NOT logged**, for the same reason: it is reachable by
  any unauthenticated peer that can open a TCP connection.
- **`worker.Handler.RegistrationTimeout` + `recvRegistration`.** The bounded `Recv` runs in a
  goroutine because grpc-go's `ServerStream` takes its deadline from the stream context, which the
  client controls. The channel is buffered so that goroutine can never block after the function
  returns, and exactly one goroutine calls `Recv` at any time.
- **`ingestLogLimiter`'s doc comment** now names the three knobs that bound its unit, does the
  fleet-wide and per-source arithmetic out loud (1024 x 16 = 16384 burst; 64 x 16 = 1024 per source),
  and states what is **not** bounded: the steady-state rate is per stream, and `MaxConcurrentStreams`
  caps concurrent streams rather than streams over a connection's lifetime.
- **README:** four new env rows, the NAT hazard with its symptom and its fix, the proxy note, the
  IPv6 asymmetry and its limits, the composite-hold arithmetic, the corrected revoked-agent paragraph,
  and the corrected auto-enroll trust-model paragraph.
- **Zero SQL, zero migration, zero proto, zero generated file, zero files under `web/`.**

## Key Decisions

- **`MaxConcurrentStreams(1)`, not a headroom value, and no env knob.** `AgentService` has exactly one
  RPC and an agent opens exactly one stream per connection, so "one stream" is a property of the wire
  contract. Headroom is not free: `ingestLogLimiter` is allocated per `Connect` call, so the value
  multiplies the per-connection log budget one for one. The brittleness is bought off with
  `TestAgentServiceHasExactlyOneStreamPerConnection`, whose failure message names the constant.
- **The keepalive policy ships as a no-op.** Documentation in code, with the derivation written down
  so the next reader does not repeat it.
- **`MaxConnectionIdle` in, `MaxConnectionAge` out.** Idle cannot terminate a connection that is doing
  its job; age can, and its cost (one dropped log chunk per forced reconnect, a `connection_epoch`
  bump, an offline/online pair) is a product decision about log fidelity, not a security decision.
- **`MaxConnectionIdle` default lowered from the spec's 15m to 60s**, because the value is the
  attacker's **duty cycle**, not a tolerance for slow middleboxes. At 15m, holding all 1024 slots cost
  1.14 new connections per second and a peer that completed the handshake parked a slot 7.5x longer
  than one that said nothing at all.
- **One key at the listener, not the item's two-phase worker/address key.** The source address covers
  the whole connection lifetime, a per-worker cap of 1 would break the reconnect overlap that
  identity-checked teardown exists to make safe, and the post-registration key would need an eviction
  path on disconnect - a teardown you can get wrong.
- **A total cap ships alongside the per-IP cap**, which the item did not propose. It is the only thing
  that yields a fleet-wide number, and the item's own last acceptance bullet requires
  `ingestLogLimiter`'s comment to cite one.
- **Auto-enroll row creation stays unbounded**, closed by README prose plus a new item, using the
  permission the item itself grants. The three candidate mechanisms are three different products and
  one of them is already an open item.

## Findings Triage

- **1 finding against the shipped control that made it defeatable by its own adversary** (the
  unauthenticated stream-parking peer), found by a Phase 4 lens. Missed by the item, the spec, the
  plan, the engineer and `/code-review`. Four prose sites in the diff asserted it could not happen.
- **1 finding that the remedy for the above composes with the idle window** rather than overlapping
  it, making the composite the cheapest parking route on the port and inverting the README comparison.
- **1 finding that the per-source cap was void against IPv6**, with `RefusedPerIP` pinned at 0 as the
  evidence.
- **3 structural guards evaded on the first attempt, in three rounds**, two of them by two lenses
  independently. All fixed; all three failure messages now describe exactly what they check.
- **4 defects introduced by the conductor** - two caught by the engineer, two by the re-verify lenses.
- **4 item claims refuted by the spec**, three of them design-changing.
- **10 spec claims refuted by the plan**, one of which (the untestable keepalive policy) changed an
  acceptance criterion.
- **1 self-caught harness defect**: a mutation battery that silently did not apply, diagnosed from
  uniformity.
- **2 `/code-review` findings; 4 HIGHs the fan-out found that it missed.**
- **0 findings against the shipped behaviour after remediation.**

## Recommended Backlog Items

**Filed this pass (proposals for human accept - the conductor commits, the human accepts):**

1. `bug-2026-08-21-netlimit-conn-wrapper-drops-tcp-user-timeout` (**bug/low**) - wrapping the accepted
   conn makes grpc-go's `SetTCPUserTimeout` type assertion on `*net.TCPConn` fail silently, and also
   empties channelz socket metrics because `syscall.Conn` is not forwarded. Both are disclosed in the
   package doc; the plan mandated an item explicitly and said it "must be tracked rather than lost in a
   doc comment", which is where it currently is. Bounded by the 40s application-layer keepalive.
2. `bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded` (**bug/medium**) - carries the
   spec's section 2.7 finding that the **enrollment-token path is already bounded**, so any fix belongs
   on the auto-enroll path and never on `UpsertWorkerByHostname`. `related` to the CIDR item and the
   hostname-takeover item, duplicating neither.
3. `idea-2026-08-21-revoked-agent-credential-survives-on-a-held-connection` (**idea/medium**) - the
   gRPC half of the same defect `idea-2026-08-09-sse-revoked-token-keeps-streaming` names on the HTTP
   side. Carries the proposed `Registry.CloseIfPresent(workerID)` and an acceptance criterion that the
   two transports agree on **one** staleness tolerance.
4. `idea-2026-08-21-netlimit-occupancy-is-unobservable` (**idea/medium**) - `Stats` is refusal counts
   only, so a saturated cap is indistinguishable from legitimate fleet growth. Names the counts-only,
   no-addresses constraint as deliberate and to be preserved, and says to spec it **with** the three
   open observability items.
5. `idea-2026-08-21-per-stream-log-budget-renewal-is-unpriced` (**idea/low**) - `ingestLogLimiter` is
   per `Connect` call, so open/burn/close/reopen yields a fresh burst per cycle;
   `authenticateAndRegister` prices it, **except** under `RELAY_ALLOW_AUTO_ENROLL`, where the
   credential is free. Already stated in the limiter's own comment; filed so it is tracked rather than
   only described.

**Considered and NOT filed, with reasons:**

- **The IPv6 delegation escape as its own item.** Aggregation raises the bar to "one host per /64 the
  attacker holds" and a `/56` or `/48` still escapes. There is no action behind a separate item: the
  only prefix length with a principled floor argument is `/64`, going coarser collapses unrelated
  operators, and the residual is already disclosed in `ipv6AggregationBits`, in `hostKey` and in
  README's row with the 16-prefix arithmetic spelled out. **The actionable half is the operator's
  inability to tell one attacker across 16 prefixes from fleet growth, and that is exactly filed item
  4's `DistinctSources` / `MaxPerSource` signature.** Cross-referenced there rather than filed.
- **`MaxConnectionAge`.** Deliberately out of scope and named as such in three source comments and in
  README. It is the mechanism half of filed item 3, which is where the decision belongs, and a separate
  item would invite somebody to set the knob without deciding the tolerance.
- **The typed-nil `net.Conn` case.** Disclosed in `Accept`'s comment with the cost of handling it
  (reflection on every accepted connection, on the hottest path the package has, for a shape no
  listener in this repo produces). An item would produce the reflection.
- **The link-local IPv6 zone collapse.** Availability-only, not an expected relay topology, and
  keying with the zone would make the key interface-specific on one side of a connection only. Stated
  in `hostKey`'s comment.
- **`bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget`** - checked and **not
  amended**. It names this slice's source item as "the real multiplier"; that multiplier is now
  bounded, which strengthens its Notes ("no live exposure") rather than changing its scope or its
  acceptance criteria. Recording the check so the next pass does not re-derive it.
- **The 701ms/704ms disagreement in `grpc_config.go`.** Two comments in the same file quote the same
  test's logged measurement with different values; the test logs whatever it measures, so neither is
  wrong and both will go stale. Flagged to the conductor as a one-line fix in this slice's own scope,
  and the right fix is the recorded one: **delete the number and cite the test**. Non-item,
  deliberately.

**Amendment applied to an existing item:**

- `idea-2026-08-09-sse-revoked-token-keeps-streaming` gains a cross-link to filed item 3 and an
  `updated:` stamp, and nothing else. No scope change, no new acceptance criterion: the unification
  requirement lives in the new item, so that the SSE item's own Done-When is not silently widened. That
  is the 2026-08-15 test applied deliberately, and it is the opposite call from the one the watchdog
  retro made on `bug-2026-08-12-retries-unvalidated-and-budget-only-in-go`, because there the two items
  would have been closed by one four-line change and here they will not be: the two transports have
  different registries, different revocation paths and different costs.

## Known Limitations

- **Connection squatting is only PARTIALLY closed.** Ending the stream hands the connection back to
  `MaxConnectionIdle`, so a peer that opens a fresh stream once per idle window still holds its slot.
  The change is more in **kind** than in cost: fire-and-forget parking is gone, any interruption
  releases the slot, and the hold now costs about eleven small frames per second on live connectivity
  rather than nothing at all and forever. `MaxConnectionAge` is the only arm that closes it and is
  deliberately out of scope.
- **`TCP_USER_TIMEOUT` is lost on Linux** whenever either cap is enabled, and channelz socket options
  go empty. Bounded by the 40s application-layer keepalive; relay registers no channelz service. Filed
  item 1.
- **The typed-nil `net.Conn` case is disclosed, not handled.**
- **Link-local IPv6 collapses to one `fe80::/64` bucket**, because `netip.Prefix` discards the zone.
  Availability-only; not an expected relay topology.
- **The new wiring guard REQUIRES the `netlimit.Config` to be a composite literal at the `Wrap` call
  site.** That is stricter than `main.go` needs and would reject a legitimate refactor that builds the
  config in a helper. Chosen deliberately as the only shape that kills the field-assignment mutation,
  and the failure message says so - but it is a constraint on future code, not only a regression guard.
- **The keepalive enforcement policy has no behavioural test at all.** A future loosening is caught
  only by a constant-lockstep assertion. Accepted because the shipped value is behaviourally identical
  to grpc-go's own default, so the worst case of the guard failing is that relay keeps the behaviour it
  already has.
- **`RELAY_GRPC_MAX_CONNS_PER_IP = 64` is not derivable from this repo.** It is a guess about NAT
  topology, documented as such, generous, and reversible with `0`.
- **A total connection cap is a denial ceiling.** An attacker that fills 1024 slots locks out
  legitimate agents earlier than the FD limit would. Mitigated by the per-source cap, by idle reaping,
  by the registration deadline, and by `0` disabling it.
- **Auto-enroll `workers` row creation remains unbounded**, and hostname takeover remains open
  (`bug-2026-08-12-auto-enroll-hostname-takeover`). This slice bounds the concurrency of row creation,
  not its total.
- **The suite figures were reported by the implementing and verifying lanes and are not re-measured
  here**: unit 511 -> 554 top-level, nine tagged integration packages green, `-race` green on the three
  changed packages. These are **top-level** counts.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **sixteenth iteration**.
  Four refutations, three design-changing.
- **A backlog proposal is not a contract** - sixteen for sixteen.
- **Plan-supplied tests and plan-supplied mutations are untrusted** - honored, and it paid twice: the
  plan refuted the spec's keepalive tests as unwritable, and the engineer refuted two conductor-supplied
  test designs before writing them.
- **A mutation battery needs a green, non-trivial baseline, and uniformity indicts the harness** -
  **recognized rather than re-learned**, one day after it was recorded. First time a mutation-harness
  lesson has paid off in the next slice.
- **A mutation proof must leave a test behind** - honored; every evasion above left a permanent guard,
  and the registration-deadline mutation left
  `TestHandler_MessageLoopRecvIsNotBoundedByTheRegistrationDeadline`.
- **A guard whose failure message promises more than the guard checks is worse than no guard** -
  honored the hard way, three times. Every replaced message now describes exactly what is checked, and
  one of them names the mutation that broke its predecessor.
- **Wrong prose about correct code is the dominant defect class** - **eleventh consecutive iteration**,
  with a new variant: four prose sites asserting a property the code did not have, plus a false
  conductor-dictated README sentence that the same file contradicted 370 lines away.
- **The correct fix for a stale count in a comment is usually to delete the count** - **not honored**;
  see the 701ms/704ms note above, flagged rather than fixed.
- **Say what a fix does not buy in the same sentence that says what it does** - honored throughout, and
  it is the single most useful convention in this diff: every one of the four controls carries a "what
  this does NOT close" paragraph, and two of them exist because an earlier version of that paragraph was
  wrong.
- **A clean `/code-review` is a lead, not a verdict** - honored; the diff shape table has a fourth row.
- **Backlog housekeeping is required scope** - the close of the source item is outstanding and belongs
  to the conductor.

New from this iteration:

- **A guard built from a mutation kill inherits the MUTATION's shape, not the DEFECT's.** Write the
  guard from the property, then search adversarially for the shape - other files, other spellings,
  other AST node kinds in the same syntactic position - and record the hit count. Three for three this
  slice. **Candidate for durable memory**, as the executable sibling of "a uniqueness claim is a claim
  about the complement".
- **A principle stated in an assertion message is not a check.** Strictly more dangerous than the
  recorded comment version, because an assertion message reads like evidence that the check exists.
- **A slice that introduces a CEILING converts every "bounded only by exhaustion" hold into a denial
  primitive.** Ask not what the cap stops but what the cap makes newly worth doing. Second consecutive
  slice defeatable by its own adversary.
- **When two bounds are configured independently, something must own the composite.** The tell that a
  composite exists: bound A's remedy hands its subject back to bound B rather than terminating it. A
  test asserting the property must assert on the sum, not on one term.
- **A consistency argument between two controls is only valid if they face the same adversary.**
  `api.clientIP` keys IPv6 exactly and is right to; copying that rule into a connection cap made the
  cap absent rather than weaker.
- **When prose is the deliverable, it gets the adversarial pass.** A correction recorded in one spec
  does not propagate to the next document describing the same mechanism.
- **The verification chain is worth its length only if each stage treats the previous stage's output as
  untrusted.** Item -> spec -> plan -> engineer -> four lenses -> two re-verify lenses, and every stage
  found errors in the one before it.

## Files Most Touched

- `internal/netlimit/listener.go` - read the package doc first (refusal-is-a-close, and the two
  wrapping consequences), then `hostKey`'s comment in full for the IPv4/IPv6 asymmetry and why it
  deliberately diverges from `api.clientIP`, then `conn.Close`'s comment for the `sync.Once` and the
  explicit argument about which CLAUDE.md invariant this is and is not.
- `cmd/relay-server/grpc_config.go` - `grpcMaxConcurrentStreams`'s comment (two legitimate reasons to
  move, each with its own guard in a different file, including the record of the vacuous one),
  `grpcKeepaliveParams`'s "NOTE WHAT `MaxConnectionIdle` DOES NOT COVER", the defaults block's "THE
  LAST TWO NUMBERS ARE NOT INDEPENDENT", and `resolveGRPCBounds`'s composite warning.
- `cmd/relay-server/grpc_config_test.go` - `TestGRPCServerOptionNamesAreUniqueAcrossThePackage`'s
  comment is the headline lesson written where the next person to widen a guard will hit it, and
  `TestGRPCAdmissionIsWiredByMain`'s check 2 is the composite-literal constraint and its reasoning.
- `internal/worker/handler.go` - `DefaultRegistrationTimeout`'s comment (why the connection caps are a
  weapon without it, what it does not close, and why raising it is a security change) and
  `recvRegistration`'s (only the first `Recv` is bounded, and the paragraph that used to be the only
  thing enforcing that).
- `internal/worker/ingest_log_limiter.go` - the "WHAT BOUNDS THE UNIT THIS BUDGET IS STATED PER" and
  "WHAT IS NOT BOUNDED" paragraphs, which are the item's acceptance bullet 5 and the source of filed
  item 5.
- `README.md` rows for the four new variables, the revoked-agent paragraph, and the auto-enrollment
  cost paragraph.
- `docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md` - sections 2.3, 2.5 and 3 are the
  reusable parts (what grpc-go actually defaults, `t.idle`'s lifecycle, and the four refutations).
- `docs/superpowers/plans/2026-08-20-grpc-admission-bounds.md` - R1 through R10, the ten spec
  refutations.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No `git log`,
  no `git diff`, no test run. Every claim below that could be checked by reading was checked against the
  worktree.
- **Verified by reading:** `internal/netlimit/listener.go` in full; the test-name list of
  `internal/netlimit/listener_test.go`; `cmd/relay-server/grpc_config.go` in full;
  `TestGRPCAdmissionIsWiredByMain` checks 1 and 2, `TestGRPCServerOptionsHasExactlyOneKeepaliveParams`,
  `TestDefaultGRPCMaxConnIdleIsTheAttackersDutyCycle` and
  `TestGRPCServerOptionNamesAreUniqueAcrossThePackage`'s comment in
  `cmd/relay-server/grpc_config_test.go`; `TestGRPCServer_RegistrationDeadlineAndIdleWindowCompose` and
  `TestGRPCServer_IdleConnectionWithNoStreamIsClosed` in `cmd/relay-server/grpc_server_test.go`;
  `main.go`'s wiring block lines 189-214; `internal/worker/handler.go`'s `DefaultRegistrationTimeout`,
  `Connect`, `recvRegistration`, `registrationTimeout` and `authenticateAndRegister`;
  `internal/worker/registry.go` in full; `internal/worker/ingest_log_limiter.go`'s doc comment;
  `internal/api/agent_enrollments.go`'s `handleDeleteWorkerToken`; `internal/store/query/workers.sql`'s
  `ClearWorkerAgentToken`; `internal/scheduler/dispatch.go`'s status filter at line 213; README lines
  272-291, 310-318 and 352-388; the source backlog item in full; the spec in full; the plan's R1-R10 and
  its backlog-items-to-file section; and the three 2026-08-20 retros for structure.
- **The conductor's false README claim was confirmed against code, not inferred:**
  `ClearWorkerAgentToken` sets `status = 'revoked'` (`workers.sql:83-86`) and `dispatch.go:213` skips
  any worker whose status is not `online` or `stale`. New dispatches stop at once. The shipped README
  paragraph now says so.
- **Duplicate check run before each filed item**, against every open item in `docs/backlog/`. Item 1 has
  no neighbour. Item 2's neighbours are `bug-2026-08-12-auto-enroll-hostname-takeover` (a different
  defect on the same path: takeover of an existing row, not creation of new ones, and its proposed fix
  does not bound new-hostname creation) and `idea-2026-06-04-cidr-allowlist-auto-enroll` (a
  trust-boundary change that would bound this as a side effect; a different product); cross-linked, not
  merged. Item 3's neighbour is `idea-2026-08-09-sse-revoked-token-keeps-streaming`; filed as a sibling
  with the unification requirement in the new item, and the SSE item amended with a cross-link only.
  Item 4's neighbours are the three open observability items; cross-linked with an explicit
  spec-together instruction. Item 5's nearest neighbours are
  `bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget` (allocation *order*, not
  renewal) and `idea-2026-08-15-ingest-log-suppression-is-uncounted` (observability); neither covers it.
- **Reported by the implementing and verifying lanes, not re-run here:** all suite counts and the
  `-race` result; the mutation results behind every guard evasion, including the three that were run to
  green; the 55s stream-parking repro and the 200ms idle-window control; the measured composite hold;
  the `RefusedPerIP == 0` observation; the two `/code-review` findings and the four fan-out HIGHs; the
  CRLF mutation-harness diagnosis; `go build` and `go vet -tags integration`.
- **Not verified:** all test results, the commit set and diff stat, and the change set as `git` sees it.
  Each is attributed above.
- **No PR number appears anywhere in this retro or in the filed items**, by instruction: the PR does not
  exist at the time of writing and a predicted number is wrong the moment a concurrent session opens one
  first. The work is referenced by date and slug.
- **The five items filed by this pass are in `docs/backlog/` as proposals**; the human gives final
  accept. **Outstanding and belonging to the conductor:** the close of
  `bug-2026-08-15-grpc-connection-admission-is-unbounded` (`/backlog close`, never a hand-edited
  `status:`) with the Resolution amendment recommended below, the 701ms/704ms one-line prose fix, the
  exact-file-set check, the final gates, all commits, and a ROADMAP refresh.
</content>
</invoke>
