# Bound gRPC connection and stream admission on the agent port

- Date: 2026-08-20
- Backlog item: `docs/backlog/bug-2026-08-15-grpc-connection-admission-is-unbounded.md`
- Verified against: worktree `pr-merge-session-961184`, branch `claude/pr-merge-session-961184`, `main` @ `4b97895`
- Dependency versions read in the module cache: `google.golang.org/grpc v1.80.0` (`go.mod:17`)
- Gate mode: autonomous. Every call recorded in section 11.

---

## 1. Problem, restated after verification

The item's headline is right and its diagnosis of the mechanism is mostly wrong.

Right: **"per connection" is only a bound if connections are bounded.** Nothing in this repo bounds
gRPC connections on `:9090`, so every per-connection control shipped on 2026-08-15
(`ingestLogLimiter`) has a multiplier the process does not control. The only ceiling today is the
process file-descriptor limit.

Wrong, and it matters for what the slice ships: three of the four things the item says are "at their
grpc-go default, therefore absent" are not absent. grpc-go applies a **5 minute keepalive
enforcement policy** whether you set one or not, `PermitWithoutStream` **is** decided (at `false`),
and a stream interceptor is structurally the wrong place for a *connection* cap. Section 3 states
each refutation with the line it was read on.

What is genuinely unbounded, after checking:

1. **Concurrent streams per connection** (`math.MaxUint32`). Real, and cheap to close.
2. **Concurrent connections, per peer and in total.** Real, and the item's actual subject.
3. **Idle transports.** Real, and it is what makes a connection cap safe to add rather than a
   denial primitive.
4. **`workers` rows under `RELAY_ALLOW_AUTO_ENROLL=true`.** Real, unbounded, persistent, and
   **out of this slice** (section 5).

This slice bounds 1, 2 and 3, and closes 4 with a written-down decision plus a new item.

---

## 2. What the code actually does, verified at HEAD

Every claim in this section was read in the tree or in the module cache.

### 2.1 The server construction, and the port topology

`cmd/relay-server/main.go:186-191`, exactly as the item quotes it:

```go
	grpcSrv := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second, // ping after 30s of transport inactivity
			Timeout: 10 * time.Second, // close the transport if no ack within 10s
		}),
	)
```

**Confirmed: exactly one option.** And it is the only one in the tree: a shape search for
`grpc.NewServer|net.Listen\(` over all `*.go` returns two production hits, `main.go:186` and
`main.go:193`, plus four in `internal/agent`'s own tests. There is no second server to keep in sync.

**HTTP and gRPC are separate listeners on separate ports, confirmed.** `main.go:193` is
`net.Listen("tcp", grpcAddr)` (`:9090`, `main.go:41-44`); `main.go:238` is a distinct
`&http.Server{Addr: httpAddr}` on `:8080` (`main.go:37-40`) with its own `ListenAndServe`. Nothing
is multiplexed. A limit applied to the gRPC listener cannot reach the SPA, the REST API or SSE, and
a limit on `MaxConcurrentStreams` cannot starve an HTTP client.

**The gRPC service has exactly one RPC.** `proto/relayv1/relay.proto:7-8`:

```proto
service AgentService {
  rpc Connect(stream AgentMessage) returns (stream CoordinatorMessage);
}
```

This is the structural fact the whole `MaxConcurrentStreams` decision rests on, and section 8.1
turns it into a test rather than a convention.

### 2.2 The agent's dial options: there is no client keepalive at all

This is the single most important verification in the spec, because the item instructs the
implementer to derive `MinTime` from "the agent's own keepalive settings in `internal/agent/`".

`internal/agent/agent.go:196-202`, the complete option list:

```go
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if dialContextFn != nil {
		opts = append(opts, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return dialContextFn(ctx)
		}))
	}
	conn, err := grpc.NewClient(a.coord, opts...)
```

A grep for `keepalive` across `internal/agent` returns **zero** hits outside test files. There is no
`grpc.WithKeepaliveParams`. So the agent runs grpc-go's client defaults, which are
(`grpc@v1.80.0/internal/transport/defaults.go:31-33`):

```go
	infinity                      = time.Duration(math.MaxInt64)
	defaultClientKeepaliveTime    = infinity
	defaultClientKeepaliveTimeout = 20 * time.Second
```

`Time = infinity` means **keepalive is disabled on the agent**: it sends no keepalive pings, ever,
with or without a stream. The item's premise for this bullet does not exist. The derivation is
therefore not "pick a number above the agent's cadence"; it is "the agent has no cadence, so the
constraint comes entirely from what grpc-go already enforces". Section 2.3.

The agent's BDP estimator does emit ping frames, but only in response to server data, and
`t.setResetPingStrikes` (`http2_server.go:271-273`, wired onto every outgoing data and header frame
at e.g. `:1046`) zeroes the strike counter on exactly that event, so a BDP ping that follows server
output cannot accrue a strike. This is true today under the default policy and stays true under an
explicit one of the same value.

### 2.3 grpc-go already enforces a keepalive policy, and it is stricter than anything we would write

`grpc@v1.80.0/internal/transport/http2_server.go:241-244`:

```go
	kep := config.KeepalivePolicy
	if kep.MinTime == 0 {
		kep.MinTime = defaultKeepalivePolicyMinTime
	}
```

`defaults.go:40`: `defaultKeepalivePolicyMinTime = 5 * time.Minute`.

`PermitWithoutStream` has no defaulting branch, so it is the zero value `false`. The enforcement
logic, `http2_server.go:891-914`:

```go
	if atomic.CompareAndSwapUint32(&t.resetPingStrikes, 1, 0) {
		t.pingStrikes = 0
		return
	}
	...
	if ns < 1 && !t.kep.PermitWithoutStream {
		if t.lastPingAt.Add(defaultPingTimeout).After(now) {
			t.pingStrikes++
		}
	} else {
		if t.lastPingAt.Add(t.kep.MinTime).After(now) {
			t.pingStrikes++
		}
	}

	if t.pingStrikes > maxPingStrikes {
		t.controlBuf.put(&goAway{code: http2.ErrCodeEnhanceYourCalm, debugData: []byte("too_many_pings"), ...})
	}
```

with `maxPingStrikes = 2` and `defaultPingTimeout = 2 * time.Hour` (`:864-865`).

So today, on HEAD, a client that pings more often than every 5 minutes while holding a silent stream
is torn down after 3 strikes, and a client that pings with **no** stream is measured against a
**2 hour** floor. The item's "a client may ping as often as it likes without being terminated" is
false.

The consequence for the design is uncomfortable and has to be stated rather than smoothed over: any
explicit `EnforcementPolicy` we write is either **identical** to today's behaviour or **looser** than
it. There is no tightening available. Section 6.2 decides what to do with that.

### 2.4 `MaxConcurrentStreams` is genuinely unset, and what "refusal" means on each side

`grpc@v1.80.0/server.go:189-190`:

```go
var defaultServerOptions = serverOptions{
	maxConcurrentStreams:  math.MaxUint32,
```

**Confirmed.** Two enforcement points follow from setting it, and they behave very differently:

- **Server transport, `http2_server.go:537-542`.** On an inbound HEADERS frame,
  `if uint32(len(t.activeStreams)) >= t.maxStreams` puts a `cleanupStream` with
  `rstCode: http2.ErrCodeRefusedStream`. The handler is never invoked, so
  `newHandlerQuota` (`server.go:1058-1063`) is never reached and its blocking `acquire()` cannot
  stall the connection's reader goroutine. Verified because it is the failure mode that would make a
  small `MaxConcurrentStreams` dangerous, and it is not reachable.
- **Client transport, `http2_client.go:829-840`.** `checkForStreamQuota` **blocks** on
  `t.streamsQuotaAvailable` when the quota is exhausted, waiting on `ctx.Done()` as the only escape.
  A compliant grpc-go client therefore **never sees the RST**; it waits for a slot.

**This is decisive for the test plan and the task brief flagged it correctly.** A test that opens
`N+1` streams with a compliant client and waits for an error hangs forever. The assertable form is a
**deadline**: the `N+1`th `Connect(ctx)` must return `codes.DeadlineExceeded` because it blocked on
quota. Section 8.1.

One more detail that makes the RED clean: the server only advertises the setting when it is not the
default, `http2_server.go:178-183`:

```go
	if config.MaxStreams != math.MaxUint32 {
		isettings = append(isettings, http2.Setting{
			ID:  http2.SettingMaxConcurrentStreams,
			Val: config.MaxStreams,
		})
	}
```

So at HEAD no `SETTINGS_MAX_CONCURRENT_STREAMS` frame is sent at all and the client falls back to
`defaultMaxStreamsClient = 100` (`defaults.go:34`). A test that opens 2 streams and expects the
second to block is unambiguously RED at HEAD, where 100 succeed.

### 2.5 Idle and age are at `infinity`, and `idle` starts armed

`defaults.go:35-37`: `defaultMaxConnectionIdle`, `defaultMaxConnectionAge` and
`defaultMaxConnectionAgeGrace` are all `infinity`. **Confirmed.**

`MaxConnectionIdle` semantics, needed to size it: `t.idle` is initialised to `time.Now()` at
transport construction (`http2_server.go:266`), cleared to the zero value when the first stream opens
(`:582-585`), and re-stamped when the last stream closes (`:1299-1306`). The idle timer
(`:1204-1220`) treats a zero `t.idle` as non-idle and reschedules. So:

- a connection holding a stream is **never** idle, no matter how long it lives or how quiet it is;
- a connection is idle from the instant the TCP transport is created until its first stream opens.

For a legitimate relay agent the second window is the time between `grpc.NewClient` dialing and
`client.Connect(connCtx)` at `agent.go:209`, which is sub-millisecond on a LAN. Any value above a
few seconds costs a legitimate agent nothing.

### 2.6 Registration, identity, and where a peer can be named

`Connect` (`handler.go:128-195`) receives the `RegisterRequest` as the first message and calls
`authenticateAndRegister` (`:140`). So **authentication happens after the stream exists**, on the
stream's first inbound message. Before that point the only identity available is the transport peer.

`peer.FromContext` **is** available and already used: `handler.go:48-54`

```go
func remoteAddr(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return "unknown"
}
```

so the item's "keyed on the authenticated worker after registration and on remote address before it"
is *implementable*. Section 6.3 rejects it anyway, for reasons that are about cost and lifetime
rather than availability.

Two facts about concurrent connections claiming the same identity, which bear on whether a per-worker
cap would even be correct:

- `Registry.Register` (`internal/worker/registry.go:27-31`) **overwrites**: `r.streams[workerID] = s`.
  N concurrent connections for one worker leave one dispatch target and N live recv loops, N
  `ingestLogLimiter`s, N transports. The multiplication the item describes is therefore fully
  available to a holder of **one** valid agent token. Confirmed.
- Teardown is identity-checked (`UnregisterIf`, `:38-46`), and a fresh registration deliberately
  coexists with a dying one until the dying one's defers run. **A per-worker connection cap of 1
  would break reconnect**, because the new connection registers before the old one has torn down.

### 2.7 Auto-enroll row creation, traced end to end

`RELAY_ALLOW_AUTO_ENROLL` gating, `main.go:151-157`: the field is set only when the env var is
non-empty, and `Handler.AllowAutoEnroll`'s zero value is `false` (`handler.go:105`). An unparseable
value is `log.Fatalf`. **Default off. Confirmed**, and README already documents it that way
(`README.md:286`).

`authenticateAndRegister` (`handler.go:198-210`) routes a credential-less register to
`autoEnrollAndRegister` only under that flag, otherwise `Unauthenticated`.

`autoEnrollAndRegister` (`:307-363`) runs, per connection, in one transaction:
`GetWorkerByHostnameForUpdate` -> revoked check -> `UpsertWorkerByHostname` -> `SetWorkerAgentToken`.
`UpsertWorkerByHostname` (`internal/store/query/workers.sql:56-68`) is
`INSERT ... ON CONFLICT (hostname) DO UPDATE`. `reg.Hostname` is caller-supplied and validated
nowhere, as the function's own comment says at `:354-356`.

**Confirmed: one persistent `workers` row per distinct hostname, unbounded, under auto-enroll.**

**The enrollment-token path does NOT have this property, which the item does not say and which
narrows the exposure.** `enrollAndRegister` (`:216-284`) calls the same `UpsertWorkerByHostname`
(`:242`), but inside a transaction that also calls `ConsumeAgentEnrollment` and returns
`errEnrollmentNotConsumable` when `rows == 0` (`:257-266`), which rolls the whole transaction back
(`:276-281`). It also rejects an already-consumed or expired token up front (`:226-231`). So one
enrollment token buys **exactly one** row, and row creation on that path is bounded by admin
issuance. `reconnectAndRegister` (`:287-302`) creates no rows at all.

The unbounded-rows exposure is therefore **specific to `RELAY_ALLOW_AUTO_ENROLL=true`**, which is
off by default and whose documented trust model is "any host able to reach gRPC is trusted"
(`README.md:286`).

### 2.8 What a legitimate agent does when a connection dies

Needed to price every option in section 6 honestly. `agent.go:97-133`:

- `connect` returns an error; if it is `codes.Unauthenticated` the agent **exits** (`:108-116`);
- otherwise, if the session had registered, backoff resets to 1s **before** sleeping (`:123-125`),
  then reconnect;
- unhealthy failures double the backoff to a 60s cap (`nextReconnectBackoff`, `:80-89`).

`connect` builds a **new** `grpc.ClientConn` per attempt and `defer conn.Close()`s it (`:202-206`),
and opens exactly one stream (`:209`). Connections are strictly sequential: there is never more than
one client connection, or more than one stream, per agent process.

Server side, on a forced disconnect: `teardownConnection` -> grace timer; on reconnect
`finishRegister` calls `h.grace.Cancel(workerID)` (`:380-382`) before anything requeues, and the
grace window is `RELAY_WORKER_GRACE_WINDOW`, default 2m (`main.go:117-122`). **So a reconnect inside
2 seconds requeues nothing. Verified, not assumed.** Runners survive because they bind to `runCtx`,
not `connCtx` (`agent.go:301-318`).

The cost that is **not** zero: `runSender` drops the one in-flight message on a stream drop and
deliberately does not re-enqueue, and task-log chunks are not replayed (`agent.go:159-166`). Every
forced reconnect can lose **one log chunk**. Plus a `connection_epoch` bump and an
offline-then-online pair on the events broker.

---

## 3. Discrepancies between the item and HEAD

Most important first. **The item's central premise survives; three of its four proposed mechanisms
do not.**

1. **REFUTED: "No `keepalive.EnforcementPolicy`, so a client may ping as often as it likes without
   being terminated, and `PermitWithoutStream` is not decided either way."** grpc-go defaults
   `MinTime` to 5 minutes (`http2_server.go:242-243`, `defaults.go:40`) and `PermitWithoutStream` to
   `false`, and a no-stream ping is measured against a 2 hour floor. Both are decided; both are
   stricter than any value an operator would plausibly write. There is nothing to tighten here.

2. **REFUTED: "Decide `MinTime` against the agent's own keepalive settings in `internal/agent/`."**
   The agent sets no keepalive settings (`agent.go:196-202`, zero `keepalive` hits in non-test
   files), so `Time = infinity` and it sends no keepalive pings at all. There is no cadence to derive
   from. The derived answer is "any `MinTime` is safe from the agent, so match grpc-go's own default
   and never go above it".

3. **REFUTED: "No unary or stream interceptor, so there is no place a per-peer connection count
   ... could live today."** True that there is no interceptor, and irrelevant: an interceptor runs
   **per stream**, after the TCP connection, the TLS handshake, the HTTP/2 preface and the transport
   goroutines already exist. A connection cap enforced there has already paid for the thing it is
   trying to refuse. The correct seam is the `net.Listener` at `main.go:193`, which is one line away
   and needs no grpc-go internals. Section 6.3.

4. **REFUTED, and it narrows the item: "the enrollment path" is not implicated in unbounded row
   growth.** The item's Repro says "With a single valid agent token (or with `RELAY_ALLOW_AUTO_ENROLL`
   on...)", and its sharper case names auto-enroll only, but the item never states that the
   token-enrollment path is bounded. It is: the upsert and the single-use consume share one
   transaction and the consume's `rows == 0` rolls the upsert back (`handler.go:257-281`). One token,
   one row. Section 2.7.

5. **CONFIRMED, and it is worse than stated: a single valid agent token is enough for the full
   multiplication.** `Registry.Register` overwrites rather than rejecting
   (`registry.go:27-31`), so N connections presenting the same token all run recv loops and all
   allocate their own `ingestLogLimiter`. The item's Repro says "a single valid agent token" and is
   right; the mechanism is worth naming because it also rules out a per-worker cap of 1.

6. **CONFIRMED: `MaxConcurrentStreams` is `math.MaxUint32`** (`server.go:190`), no
   `SETTINGS_MAX_CONCURRENT_STREAMS` is advertised (`http2_server.go:178-183`), and
   `MaxConnectionIdle`/`Age`/`AgeGrace` are all `infinity` (`defaults.go:35-37`).

7. **CONFIRMED: auto-enroll creates one unbounded persistent `workers` row per distinct hostname**
   (`handler.go:307-363`, `workers.sql:56-68`), gated by a flag that defaults to off.

8. **CONFIRMED, and the item is right to insist on it: `MaxConcurrentStreams` alone closes
   nothing.** The multiplication is per connection. A cap of 1 stream per connection against an
   attacker willing to open connections is a factor-of-one improvement.

9. **New, not in the item: a global connection cap is the only thing that produces a citable
   number.** The item's last acceptance bullet requires `ingestLogLimiter`'s comment to cite
   "whatever bound lands". A per-peer cap alone does not bound the fleet-wide total, so it yields no
   number to cite. This is what promotes the global cap from nice-to-have to load-bearing
   (section 6.3).

---

## 4. Threat model and honest exposure

**Principal.** Anything that can reach `:9090`. With a valid agent token it can register; with
`RELAY_ALLOW_AUTO_ENROLL=true` it needs no credential; with **no** credential and auto-enroll off it
still gets a TCP connection, an HTTP/2 transport, server goroutines and a recv loop up to the point
`authenticateAndRegister` rejects it - and then it can immediately do it again.

**What is unbounded today.**

| Resource | Bound today | Multiplier |
| --- | --- | --- |
| Concurrent streams on one connection | `math.MaxUint32` | per connection |
| Concurrent connections, one source IP | none | none |
| Concurrent connections, all sources | process FD limit | none |
| Idle transports (no stream, ever) | none | none |
| `ingestLogLimiter` budgets | 16 burst, 6/min **each** | per stream |
| Recv goroutines, in-flight pool statements | 1 in-flight statement each | per stream |
| `workers` rows (auto-enroll on) | none | per distinct hostname |

**What is already bounded and must not be claimed as part of the fix.** Per-connection DB
concurrency: the recv loop is synchronous, so one stream has at most one in-flight statement, and
`RELAY_DB_MAX_CONNS` (25 by default, `main.go:55-60`) caps the aggregate. Message *rate* per
connection is unbounded and stays unbounded; that is the deferred recv-loop limiter, still out of
scope.

**Severity, honestly. Medium, and the item's two counter-arguments hold.** The agent port's
documented trust model is network reachability; anyone with a token already gets task dispatch;
grpc-go's defaults are what most services ship. What raises it above "hardening" is that a control
shipped four days earlier states a security property that this gap silently invalidates, and that
`workers`-row growth under auto-enroll exceeds what the trust model grants: "any reachable host may
join the pool" is not "any reachable host may create unbounded rows in the pool".

**The tradeoff a cap introduces, stated up front because it is real.** A total connection cap
converts unbounded growth into a bounded refusal, and a bounded refusal is a denial ceiling: an
attacker that fills it locks out legitimate agents at 1024 connections instead of at the FD limit.
The mitigations are the per-IP cap (one source cannot fill the global budget alone) and
`MaxConnectionIdle` (parked connections that never open a stream are reaped rather than held
forever). Both are in this slice for exactly that reason, and the global cap is `0`-disablable for
operators who would rather have the kernel's ceiling.

---

## 5. Scope decision: three of the item's five bullets ship, two do not

**In this slice:** `MaxConcurrentStreams`, an explicit `KeepaliveEnforcementPolicy`,
`MaxConnectionIdle`, a per-IP and total connection cap at the listener, the env knobs and startup
line for them, the README prose, and the `ingestLogLimiter` comment amendment.

**Out, with reasons and follow-up items.**

### 5.1 `MaxConnectionAge` / `MaxConnectionAgeGrace`: OUT, its own item

The item lists it under Proposal and, tellingly, **not** under Acceptance. It should stay out here:

- **It is not an admission control.** It bounds how long an *admitted* connection lives. Its stated
  value in the item is "forces periodic re-authentication, which also bounds how long a revoked
  credential's live connection survives" - which is a different defect, and the item itself names
  `idea-2026-08-09-sse-revoked-token-keeps-streaming` as "the same shape on the HTTP side". Two
  transports, one defect, one item.
- **Its cost is real and this slice cannot measure it.** Verified in section 2.8: a forced GOAWAY
  costs at most one dropped log chunk per reconnect (`agent.go:159-166`), a `connection_epoch` bump,
  and an offline/online pair on the broker; it requeues nothing, because `finishRegister` cancels
  the grace timer (`handler.go:380-382`) far inside the 2m window. At a 30m age over a 100-agent
  fleet that is roughly 3.3 forced reconnects a minute, each able to lose a log chunk. That is a
  product decision about log fidelity, not a security decision.
- **Defaulting it off would satisfy nothing.** A knob that ships disabled bounds nothing, and it
  would let the slice claim a control it does not exercise.

**Recommend filing:** `bug-2026-08-20-revoked-credential-survives-on-a-held-connection`, covering
both the gRPC agent stream and `/v1/events`, superseding or absorbing
`idea-2026-08-09-sse-revoked-token-keeps-streaming`. It should decide one staleness tolerance for
both transports rather than picking `MaxConnectionAge` on one and periodic re-validation on the
other.

### 5.2 Auto-enroll row creation: OUT, its own item, closed here by written decision

The item explicitly permits this ("Decide whether this is part of this item or its own; the
connection cap and the row cap are separable") and explicitly permits closing the criterion with
README prose.

Reasons to split:

- The three candidate mechanisms are three different products. A rate limit on the auto-enroll path
  throttles a storm but not a slow drip. A total-workers ceiling is a **denial primitive against
  legitimate fleet growth** and needs an operator story for hitting it. A CIDR allowlist changes the
  trust boundary and **already exists as `idea-2026-06-04-cidr-allowlist-auto-enroll`**, so absorbing
  it here would duplicate an open item.
- The exposure is gated behind a flag that is off by default and whose documented model is total
  network trust (section 2.7), so it is not urgent enough to force a mechanism choice inside a slice
  about admission.
- This slice does bound the *concurrency* of row creation, which is worth stating precisely rather
  than overclaiming: with `RELAY_GRPC_MAX_CONNS_PER_IP=64`, one source address can have at most 64
  registrations in flight at once, each requiring a full transaction. It does **not** bound the
  total, because rows survive their connections.

**Recommend filing:** `bug-2026-08-20-auto-enroll-worker-row-creation-is-unbounded`, `related` to
the CIDR item and to this spec, carrying the section 2.7 finding that the enrollment-token path is
already bounded (so any fix belongs on the auto-enroll path specifically, not on
`UpsertWorkerByHostname`).

**And the README decision that closes acceptance bullet 4 here:** the auto-enroll trust-model
paragraph gains an explicit sentence in the item's own terms, saying that a reachable host may create
one persistent `workers` row per distinct hostname it claims, that these rows survive the connection
and a restart and appear in every `GET /v1/workers` page, and that nothing bounds the total.

---

## 6. Design

### 6.1 `grpc.MaxConcurrentStreams(1)`, pinned by a structural guard

**Decision: `1`, not a small single-digit number.**

The agent uses exactly one stream (section 2.8), and `AgentService` has exactly one RPC
(section 2.1), so "one stream" is a structural property of the wire contract, not a convention. Any
headroom value is a number nobody can defend, and headroom is not free here: `ingestLogLimiter` is
allocated **per `Connect` call**, that is per stream (`handler.go:172`), so `MaxConcurrentStreams`
multiplies the per-connection log budget one-for-one. `4` would mean four budgets per connection for
no benefit.

The obvious objection is that `1` is brittle: if someone adds a second RPC to `AgentService`, a
compliant client will **block silently** on stream quota (section 2.4) rather than erroring, which is
a miserable thing to debug. The answer is a guard, not headroom:

**`TestAgentServiceHasExactlyOneStreamPerConnection`** asserts
`len(relayv1.AgentService_ServiceDesc.Methods) == 0` and
`len(relayv1.AgentService_ServiceDesc.Streams) == 1`, with a failure message that names
`grpcMaxConcurrentStreams` and says to raise it. Adding an RPC turns the surprise from a production
hang into a red test naming the fix. This is the same move as the module-wide structural guard the
job-retry slice shipped.

**No env knob.** There is no operational reason for this value to move, raising it re-opens the
multiplier, and the only legitimate reason to change it is a proto change, which the guard catches.
Same reasoning as the `ingestLogLimiter` constants.

**Cost to a legitimate agent: zero.** It opens one stream per connection and closes the connection
before opening another.

### 6.2 `KeepaliveEnforcementPolicy`: set it explicitly, at grpc-go's own value, and say why

**Decision: `keepalive.EnforcementPolicy{MinTime: 5 * time.Minute, PermitWithoutStream: false}`,
which is a behavioural no-op, shipped as documentation-in-code.**

The derivation, stated so the next reader does not repeat it:

- The agent sends no keepalive pings (`Time = infinity`, section 2.2), so **no `MinTime` can harm a
  legitimate agent**. The constraint does not come from the agent at all.
- grpc-go already enforces `MinTime = 5m` (section 2.3). Any smaller value is a **loosening**. Any
  larger value would start refusing pings grpc-go accepts today, with no principal that sends them,
  so it buys nothing and adds a novel failure mode for a future agent that does enable keepalive.
- Therefore 5 minutes is not picked, it is the unique non-regressive value, and it equals
  `defaultKeepalivePolicyMinTime`.

Value of shipping a no-op: it converts an invisible library default into a stated decision that a
future diff must argue with. The realistic failure mode this prevents is somebody "adding a keepalive
policy" with `MinTime: 10 * time.Second` because that is what the internet suggests, silently
loosening a control by a factor of 30.

The comment must say all of that, including the sentence that this is deliberately equal to
grpc-go's default and that **lowering it is the only way to make it matter, and is a regression**.

**Cost to a legitimate agent: exactly zero, provably**, because it does not send keepalive pings and
because BDP pings reset the strike counter (section 2.2).

**Testability, stated honestly and flagged in section 8.4: acceptance criterion 2 cannot be met with
a RED-at-HEAD test.** The behaviour it asks for already exists. What ships is a characterization
test that is GREEN at HEAD, whose value is the mutation kill.

### 6.3 Connection admission: a limiting `net.Listener`, keyed on the TCP source IP

**Decision: a new `internal/netlimit` package with one type, wrapping `main.go:193`'s listener.
Two caps, both `0`-disablable: a total and a per-source-IP.**

Why the listener and not an interceptor or a tap handler:

- It runs at `Accept`, before the HTTP/2 preface, the transport goroutines, the recv loop and any DB
  work. It is the cheapest possible point of refusal.
- It needs no grpc-go internals and no experimental API (`grpc.InTapHandle` is marked EXPERIMENTAL).
- It is a `net.Listener`, so it is unit-testable with a fake listener and no gRPC at all, and the
  wiring is one line in `main.go`.
- It is transport-honest: `net.Conn.RemoteAddr()` is the kernel's view of the peer. This is the same
  stance `ratelimit.go:44` takes for HTTP ("RemoteAddr is used directly, X-Forwarded-For is NOT
  trusted"), so relay ends up with one notion of "peer" rather than two.

**Why one key, not the item's two-phase key.** The item proposes "keyed on the authenticated worker
after registration and on remote address before it". Rejected:

- A connection's source address does not change at registration, so the pre-registration key already
  covers the whole lifetime. The second key adds no bound.
- The post-registration key would need shared state with an eviction path on disconnect, which is
  exactly the design `ingestLogLimiter` rejected and for the same reason: a teardown you can get
  wrong.
- A per-worker cap of 1 is **incorrect**, because a reconnecting agent legitimately overlaps a
  registered-but-not-yet-torn-down predecessor (section 2.6). Any value above 1 is arbitrary.
- The per-worker cap would fire after the attacker has already paid for the transport, the recv
  goroutine, the register transaction and possibly a new `workers` row.

**Key on the host part of `RemoteAddr`, never the full `host:port`.** Every connection has a distinct
source port, so keying on `RemoteAddr().String()` makes the per-IP cap a no-op that still passes a
naive test. This is a named mutation in section 8.6.

**Behaviour on refusal.** `Accept` returns the over-limit connection to nobody: it accepts, closes,
increments a counter, and loops to the next `Accept`. It must **never return an error** for an
over-limit peer. `grpc.Server.Serve` treats a non-temporary `Accept` error as fatal and returns, so a
refusal expressed as an error would take the entire listener down, which is the exact opposite of the
control. This is the single most dangerous way to implement this and belongs in the type's doc
comment.

**Accounting.** `Accept` returns a wrapped `net.Conn` whose `Close` decrements the total and the
per-IP count exactly once (`sync.Once`), and deletes the map entry at zero so the map does not grow
per distinct source address. `Close` on the limiter closes the underlying listener so
`grpcSrv.GracefulStop()` still works.

**Values, and the honest admission that one of them is not derivable.**

| Knob | Default | Derivation |
| --- | --- | --- |
| `RELAY_GRPC_MAX_CONNS` | `1024` | Far above any plausible relay fleet and far below where FDs or goroutines hurt. Anchor: `RELAY_DB_MAX_CONNS` defaults to 25, and each connection holds at most one in-flight statement, so more than ~25 simultaneously busy agents already queue on the pool. `0` disables. |
| `RELAY_GRPC_MAX_CONNS_PER_IP` | `64` | **Not derivable.** One agent process is strictly one connection at a time (section 2.8), so the legitimate maximum is "how many agent processes share a source address", which depends on NAT topology this repo cannot see. 64 is chosen generously and reversibly. `0` disables. |
| `RELAY_GRPC_MAX_CONN_IDLE` | `15m` | A legitimate agent's idle window is the sub-millisecond gap between dial and `client.Connect` (section 2.5), so the value is bounded below only by paranoia about slow middleboxes. 15m matches `DefaultTrailingLogWindow`'s order of magnitude and is four orders above the real window. `0` disables. |

**The NAT hazard is the one real cost to a legitimate deployment and README must name it:** a site
running more than `RELAY_GRPC_MAX_CONNS_PER_IP` agents behind a single NAT gateway will see agents
refused and reconnect-backoff indefinitely. The symptom is agents that never appear online while the
server logs refusals; the fix is to raise or disable the knob.

**Cost to a legitimate agent below the caps: zero.** Above them: the connection is closed
immediately after accept, the client sees `codes.Unavailable`, `connect` returns a non-`Unauthenticated`
error, and the reconnect loop backs off from 1s to a 60s cap (`agent.go:126-131`). Verified, not
assumed. Nothing requeues, because nothing registered.

### 6.4 `MaxConnectionIdle`, and why it belongs with the caps rather than with `MaxConnectionAge`

**Decision: set it. `RELAY_GRPC_MAX_CONN_IDLE`, default 15m, `0` disables.**

It is in scope for a reason that only exists once the caps exist: **a connection cap without idle
reaping is a parking primitive.** An attacker can complete `Accept` and the HTTP/2 preface, never
open a stream, and hold exactly `RELAY_GRPC_MAX_CONNS_PER_IP` slots forever, denying legitimate
agents from that source and consuming a slice of the global budget. `MaxConnectionIdle` reaps
precisely those and nothing else, because a connection holding a stream is never idle (section 2.5).

It is emphatically **not** `MaxConnectionAge` and must not be conflated with it: it cannot terminate
a connection that is doing its job, so it has none of the reconnect-churn cost that put
`MaxConnectionAge` in section 5.1.

**Cost to a legitimate agent: zero**, and provably so from `t.idle`'s lifecycle.

### 6.5 Observability: a startup line and a periodic refusal summary, both bounded by construction

Two additions, no more.

**A startup line**, unconditional, mirroring `watchdogBoundsLine` (`watchdog_config.go:88-103`) and
its rationale that a mechanism which can refuse a user's work should state its limits at every boot.
It names the effective total cap, per-IP cap, idle timeout and stream cap, and says explicitly when a
cap is disabled. Env parsing follows `parseWatchdogDuration`'s three-outcome shape: unset or valid is
silent, `0` is accepted and returns an informational line naming what is now unbounded, unparseable
or negative keeps the default and says so. Not `log.Fatalf`: a bad limit must not stop a server
booting when a safe default exists.

**A periodic refusal summary, not a line per refusal.** A `log.Printf` on each refusal would be a new
attacker-driven, unbounded log site on the exact path this slice exists to bound - the 2026-08-15
lesson, one layer down. Instead `netlimit` keeps two `atomic.Uint64` counters (refused-total,
refused-per-IP) and `cmd/relay-server` runs a 1-minute ticker, in the shape of
`runEnrollmentJanitor` (`main.go:269-282`), that logs **one line only when a counter moved since the
last tick**. Bounded at one line per minute, keyed on nothing, and it hands an operator a rate rather
than a stream. The line names counts, not addresses, so it cannot be used to write attacker-chosen
bytes into the log.

### 6.6 The `ingestLogLimiter` doc comment amendment

Acceptance bullet 5, and it is the reason the global cap is load-bearing rather than decorative.
`internal/worker/ingest_log_limiter.go:5` opens "bounds caller-driven log volume for ONE agent
connection" and nothing tells the reader what bounds connections. The amendment adds a paragraph
that:

- names `RELAY_GRPC_MAX_CONNS` and `RELAY_GRPC_MAX_CONNS_PER_IP` as the bound on the unit this
  budget is stated per, and `grpcMaxConcurrentStreams = 1` as the reason a connection has exactly one
  budget rather than `MaxUint32` of them;
- does the arithmetic out loud, because a bound nobody has multiplied is not yet a claim: at the
  defaults the fleet-wide worst case is `1024 x 16 = 16384` lines of burst and `1024 x 6 = 6144`
  lines per minute of steady state, and per source address `64 x 16 = 1024` burst and `384` per
  minute;
- says that raising `RELAY_GRPC_MAX_CONNS` scales that linearly, so the two knobs are part of this
  control's threat model and not merely capacity settings;
- cites this spec.

---

## 7. Alternatives considered and rejected

- **`MaxConcurrentStreams` alone.** The item forbids it in as many words and is right: the
  multiplication is per connection. Shipped, but never as the whole slice.
- **A per-peer cap in a stream interceptor**, the item's proposal. Rejected: an interceptor runs
  after the transport exists, so it refuses nothing that has not already been paid for, and it cannot
  see a connection that never opens a stream. Section 6.3.
- **`grpc.InTapHandle`.** Runs earlier than an interceptor and has the peer, but it is marked
  EXPERIMENTAL, is still per stream rather than per connection, and is a grpc-go-specific seam that
  cannot be tested without a real server. Rejected in favour of a `net.Listener`.
- **"It belongs at a reverse proxy."** The item's own alternative for this criterion. Rejected as the
  *only* answer: README's proxy guidance covers TLS termination for **HTTP :8080** only
  (`README.md:1501-1527`), the agent dials with `insecure.NewCredentials()` (`agent.go:196`), and
  nothing in the deployment story puts anything in front of `:9090`. Recommending one would introduce
  a new deployment assumption to close a gap an in-process listener closes in a testable package.
  README will still say that an operator who *does* front `:9090` should set the caps there too and
  may set `0` here.
- **A per-worker connection cap.** Rejected on correctness (breaks reconnect overlap), on cost (fires
  after the expensive part), and on lifetime (needs eviction on disconnect). Section 6.3.
- **`MaxConnectionAge`.** Section 5.1: not admission control, real fidelity cost, and it is the same
  defect as the SSE revoked-token item. Its own item.
- **A tighter `KeepaliveEnforcementPolicy` than grpc-go's default.** Rejected: no principal sends
  keepalive pings, so it buys nothing and adds a failure mode for a future agent that enables them.
  Section 6.2.
- **Env knobs for `MaxConcurrentStreams` and `MinTime`.** Rejected: neither has an operational reason
  to move, and both knobs would only let an operator loosen a security control. The three knobs that
  do ship are the ones whose right value genuinely depends on the deployment (fleet size, NAT
  topology, middlebox latency), which is the project's stated convention for operational limits.
- **Bounding auto-enroll rows here.** Section 5.2: three different products, one of them already an
  open item.
- **A log line per refused connection.** Rejected: a new unbounded attacker-driven log site inside
  the slice that bounds attacker-driven log volume. Section 6.5.

---

## 8. Test strategy

The project's rule is that a green test can be vacuous. Each criterion below names what proves it,
and where nothing can, it says so.

New home for the server construction: `cmd/relay-server/grpc_config.go`, mirroring
`watchdog_config.go`, exposing the option list so a test can build a real server from the real
options. Tests live in `cmd/relay-server/grpc_config_test.go` and use a stub `AgentServiceServer`
whose `Connect` blocks on `stream.Context().Done()`, so **no database is required** and these stay in
the default `make test` lane, not the integration lane.

### 8.1 `MaxConcurrentStreams`, and the hang trap

1. **`TestGRPCServer_SecondStreamOnOneConnectionBlocks`.** Real server on
   `net.Listen("tcp","127.0.0.1:0")` with the production options, one grpc-go client
   (`grpc.NewClient`, so one transport). Open stream 1 and hold it. Open stream 2 with a
   `context.WithTimeout(ctx, 2*time.Second)`. Assert `status.Code(err) == codes.DeadlineExceeded`.
   **A raw "expect an error" assertion here would hang forever** (section 2.4): a compliant client
   blocks on stream quota rather than erroring, so the deadline is not a convenience, it is the
   assertion.
   **RED at HEAD:** with no `SETTINGS_MAX_CONCURRENT_STREAMS` advertised the client's default is 100,
   so stream 2 opens immediately and the call returns `nil` error. Unambiguous.
2. **`TestAgentServiceHasExactlyOneStreamPerConnection`.** The structural guard of section 6.1, over
   `relayv1.AgentService_ServiceDesc`. Not RED at HEAD by design: it is a tripwire, and its failure
   message is the deliverable.
3. **Not testable, stated rather than faked:** the server-side `RST_STREAM` with `REFUSED_STREAM`
   (`http2_server.go:537-542`) is unreachable from a compliant client and asserting it would require
   a hand-rolled `x/net/http2` client, promoting an indirect dependency to a direct one to test
   library behaviour that grpc-go's own `TestMaxStreams` (`transport_test.go:1039-1112`) already
   covers. Relay asserts the advertisement's consequence, not grpc-go's transport.

### 8.2 The connection caps

4. **`TestLimitListener_RefusesBeyondPerIPCap`** (`internal/netlimit`, fake listener). Per-IP cap 2,
   total 100, three conns from `10.0.0.1:1001/1002/1003`. Assert the first two are returned from
   `Accept` and the third is closed and never returned, and that `Accept` returned **no error**.
5. **`TestLimitListener_PerIPCapIsKeyedOnHostNotHostPort`.** Three conns from the same IP on three
   different ports against a per-IP cap of 2. This is the discriminating test for the section 6.3
   keying bug: a `RemoteAddr().String()` key makes the cap a no-op and every other test still passes.
6. **`TestLimitListener_CloseReleasesTheSlot`.** Fill the per-IP cap, close one returned conn, assert
   a new conn from that IP is admitted. Plus: close the same conn twice, assert the count does not go
   negative and a second slot does not appear (the `sync.Once`).
7. **`TestLimitListener_ReleasedIPIsRemovedFromTheMap`.** Admit and close conns from 1000 distinct
   IPs; assert the internal map is empty. Without this the limiter is itself unbounded memory growth
   keyed on attacker-chosen source addresses, which would be the same defect one layer down.
8. **`TestLimitListener_TotalCapRefusesAcrossDistinctIPs`.** Total 3, per-IP 100, four conns from
   four IPs.
9. **`TestLimitListener_ZeroDisables`.** Both knobs at 0, 200 conns from one IP, all admitted.
10. **`TestLimitListener_AcceptErrorFromUnderlyingListenerPropagates`.** The one case where `Accept`
    **must** return an error, so the "never return an error" rule cannot be over-applied into
    swallowing a real listener failure and spinning.
11. **`TestGRPCServer_ConnectionBeyondCapIsRefused`** (`cmd/relay-server`, real TCP). The end-to-end
    arm, so the caps are proven **wired** and not merely implemented: wrap a real listener at per-IP
    cap 2, serve, dial three raw `net.Dial`s from loopback, assert the third returns EOF or a reset
    on a read with a deadline. **RED at HEAD:** all three connections stay open.
    The unit tests above are vacuously RED at HEAD (new package), so this is the test that carries
    the criterion; section 8.6's mutations are what make the unit tests load-bearing.

### 8.3 `MaxConnectionIdle`

12. **`TestGRPCServer_IdleConnectionWithNoStreamIsClosed`.** Build the server with a 200ms idle
    timeout injected through the option builder, dial, open no stream, assert the client transport
    goes down inside ~2s. **RED at HEAD:** `infinity`, so it never closes and the test fails on its
    timeout.
13. **`TestGRPCServer_ConnectionHoldingAStreamIsNotIdle`.** Same 200ms timeout, but open a stream and
    keep it silent for 1s. Assert the stream is still alive. This is the test that proves the section
    2.5 reading of `t.idle` and prices the option at zero for a legitimate agent. It is the one that
    would catch a future change to `MaxConnectionAge`-style semantics slipping in under this name.

### 8.4 The keepalive policy: flagged, because it cannot be proven RED

**Acceptance criterion 2 of the item cannot be met as written, and this spec says so rather than
inventing a test that passes vacuously.** The policy value we ship is behaviourally identical to
grpc-go's default (section 2.3), so no test can be RED at HEAD for it, and the "legitimate agent's
cadence is not terminated" test is vacuous in a second way: the agent has no cadence (section 2.2),
so a test that a non-pinging client survives passes against literally any policy.

What ships instead:

14. **`TestGRPCServer_AbusivePingerIsTornDown`**, a characterization test that is **GREEN at HEAD**
    and labelled as such in its own comment. Client with
    `keepalive.ClientParameters{Time: 100 * time.Millisecond, PermitWithoutStream: true}`, transport
    forced up with `conn.Connect()` and a wait for `READY`, no stream opened. Three strikes accrue in
    ~300ms against the no-stream `defaultPingTimeout` branch and the server sends
    GOAWAY `too_many_pings`. Assert the connection drops.
    **Its value is the mutation kill:** set `MinTime` to `time.Second` and the test still passes
    (that branch does not consult `MinTime`), so this test alone is not enough, which is why 15
    exists.
15. **`TestGRPCServer_PingerInsideAnOpenStreamIsTornDown`.** Same client cadence, but with an open
    stream so the server takes the `t.kep.MinTime` branch (`http2_server.go:904-908`). This one
    **does** go RED when `MinTime` is mutated to `100 * time.Millisecond`, which is the mutation that
    matters, because loosening is the only realistic future regression.
16. **`PermitWithoutStream: false` is NOT independently testable in reasonable time, and this is
    recorded rather than papered over.** Distinguishing it from `true` requires a client pinging
    slower than `MinTime` (5 minutes) with no stream, so any honest test takes over ten minutes. It
    ships as a written-down decision pinned by an assertion on the constructed
    `keepalive.EnforcementPolicy` value, and that assertion is acknowledged as a constant check that
    proves nothing about behaviour.

### 8.5 Env parsing and the startup line

17. **`TestParseConnLimit`**, table-driven over unset / valid / `0` / negative / unparseable, mirroring
    the existing `watchdog_config_test.go` shape, asserting both the value and the presence or
    absence of the message.
18. **`TestGRPCBoundsLine`**, over all-enabled / per-IP disabled / total disabled / both disabled,
    mirroring `watchdogBoundsLine`'s tests.
19. **`TestRefusalSummaryLogsOnlyWhenCountersMove`**, driving the reporter's tick directly. Pins the
    section 6.5 property that the refusal log is bounded at one line per interval, which is the thing
    that would otherwise reintroduce the defect the previous slice closed.

### 8.6 Mutation matrix

A test is load-bearing only if a mutation kills it.

| Mutation | Must go RED |
| --- | --- |
| Remove `grpc.MaxConcurrentStreams` from the option list | 1 |
| Raise `grpcMaxConcurrentStreams` to 2 | 1 |
| Add a second RPC to `AgentService` | 2 (with a message naming the constant) |
| Key the per-IP map on `RemoteAddr().String()` | **5** |
| Drop the per-IP cap, keep the total | 4, 5, 11 |
| Drop the total cap, keep the per-IP | 8 |
| Return an `error` from `Accept` instead of closing and looping | 11 (server stops serving) |
| Never delete the per-IP map entry at zero | **7** |
| Decrement on every `Close` instead of once | 6 |
| Remove `MaxConnectionIdle` from the option list | 12 |
| Give `MaxConnectionIdle` age semantics (close regardless of streams) | **13** |
| `MinTime: 100 * time.Millisecond` | **15** (and NOT 14, which is the point) |
| Log one line per refusal instead of a periodic summary | 19 |

Mutations must be run in an isolated worktree, not the shared tree.

### 8.7 Existing tests

No existing test should move. `cmd/relay-server`'s current tests do not construct a gRPC server, and
`internal/worker`'s tests call handlers directly with no transport (a grep for
`grpc.NewServer|bufconn` in `internal/worker` returns zero hits). **Any existing test whose result
changes is a finding to report, not to fix.**

---

## 9. Constraint checks

- **Epoch fence.** This slice adds no write to `tasks.status` or `task_logs`, no SQL, no migration,
  no generated file. It changes no predicate and no fence argument. If a plan step proposes
  `make generate`, that step is wrong.
- **Single job-spec pipeline.** Not applicable; no spec ingestion.
- **One bounded sender per gRPC stream.** Untouched. No send is added anywhere, and
  `MaxConcurrentStreams = 1` strengthens the invariant's premise by making "one stream per
  connection" enforced rather than conventional.
- **Identity-checked teardown.** Untouched, and deliberately: section 6.3 rejects the per-worker cap
  partly *because* it would collide with the reconnect overlap this invariant exists to make safe.
  The limiter's own teardown is per connection and idempotent (`sync.Once`), with no shared identity
  to clobber.
- **No interior pointers across locks.** The per-IP map is guarded by one mutex and never yields a
  pointer; the wrapped conn holds a count key (a string) and a `*Limiter`, and mutates only through
  methods that take the lock. Counters are `atomic.Uint64` read by value.
- **Single JSON entry point.** Not applicable.
- **End the generation before releasing the resource.** The wrapped conn decrements **after** the
  underlying `Close` returns, so a slot is never handed out while its predecessor's FD is still open.
- **No new attacker-driven log site.** Section 6.5. This is the constraint most likely to be violated
  by a well-meaning implementation, and test 19 is its guard.
- **Generated code.** None touched.

---

## 10. Scope

**In.**

- `cmd/relay-server/grpc_config.go` (new): the option builder, the env parsers, the bounds line.
- `cmd/relay-server/main.go`: wrap `grpcLis` with the limiter, use the option builder, log the bounds
  line, start the refusal-summary reporter.
- `internal/netlimit/` (new): the limiting listener and its counters.
- `internal/worker/ingest_log_limiter.go`: the doc comment amendment of section 6.6. **Comment only,
  zero behaviour change.**
- `README.md`: three new rows in the `relay-server` env table; the NAT hazard and the proxy note; the
  auto-enroll trust-model sentence of section 5.2.
- Tests of section 8.

**Out, explicitly.**

- `MaxConnectionAge` / `MaxConnectionAgeGrace` and revoked-credential lifetime. Section 5.1, its own
  item, to be unified with `idea-2026-08-09-sse-revoked-token-keeps-streaming`.
- Any bound on auto-enroll `workers` row creation. Section 5.2, its own item; the CIDR allowlist
  remains `idea-2026-06-04-cidr-allowlist-auto-enroll` and is not duplicated here.
- The recv-loop message-rate limiter. Still deferred, still unfiled, and still the honest answer to
  per-connection message volume. This slice bounds the *number of connections*, which is its stated
  dependency.
- TLS on `:9090`. The agent dials `insecure` (`agent.go:196`); that is a separate decision and this
  slice does not touch it.
- IPv6 /64 aggregation for the per-IP key. Keying is per exact address, matching
  `api.clientIP`'s rule. A host with privacy extensions can present many addresses, so the per-IP cap
  is weaker for IPv6 than for IPv4. Documented as a limitation; not fixed here.
- `bug-2026-08-12-auto-enroll-hostname-takeover`. Adjacent, same trust model, untouched.

---

## 11. Decisions taken autonomously

Gate mode is autonomous; these would otherwise have been questions.

- **D1. `MaxConcurrentStreams = 1`, not a headroom value.** Section 6.1. Would have escalated,
  because `1` is the value most likely to bite a future contributor. Called: `1`, because the proto
  makes it structural and because headroom multiplies `ingestLogLimiter` budgets one-for-one; the
  brittleness is bought off with a structural guard test that names the constant in its failure
  message.
- **D2. The `KeepaliveEnforcementPolicy` ships as a behavioural no-op at grpc-go's own 5m default.**
  Sections 2.3 and 6.2. Would have escalated: it refutes the item's premise and ships a line that
  changes nothing. Called: ship it, because the alternative values are "looser" and "pointlessly
  stricter", and because an explicit value is what makes a future loosening visible in a diff.
- **D3. The item's acceptance criterion 2 cannot be met with a RED-at-HEAD test, and the item should
  be amended to say so.** Section 8.4. Would have escalated: it changes the item's own Done-When.
  Called: strike the RED requirement for that criterion, keep the policy and the two characterization
  tests, and record that `PermitWithoutStream: false` is not independently testable in reasonable
  time.
- **D4. The per-peer cap is a `net.Listener`, keyed on the TCP source IP only, not the item's
  two-phase worker/address key.** Section 6.3. Would have escalated: it contradicts the item's
  proposal directly. Called: one key, at `Accept`, because the address covers the whole connection
  lifetime, because a per-worker cap of 1 would break reconnect overlap, and because an interceptor
  refuses nothing that has not already been paid for.
- **D5. A TOTAL connection cap ships alongside the per-IP cap**, which the item does not propose.
  Would have escalated as a scope extension. Called: include, because it is the only thing that
  yields a fleet-wide number, and acceptance bullet 5 requires `ingestLogLimiter`'s comment to cite
  one. Without it that bullet can only be met with a shrug.
- **D6. `MaxConnectionIdle` is in scope; `MaxConnectionAge` is not.** Sections 5.1 and 6.4. Would
  have escalated: the item lists them together. Called: split them, because idle cannot terminate a
  working connection and age can, and because a connection cap without idle reaping is a parking
  primitive that this slice would otherwise create.
- **D7. Auto-enroll row bounding is closed here by README prose plus a new item, not by code.**
  Section 5.2. Would have escalated as a scope decision. Called: split, using the permission the item
  itself grants, because the three candidate mechanisms are three different products and one of them
  is already an open item.
- **D8. Three env knobs (`RELAY_GRPC_MAX_CONNS`, `RELAY_GRPC_MAX_CONNS_PER_IP`,
  `RELAY_GRPC_MAX_CONN_IDLE`), and none for the stream cap or `MinTime`.** Section 6.3. The three
  that ship have deployment-dependent right answers (fleet size, NAT topology, middlebox latency);
  the two that do not could only be loosened.
- **D9. `RELAY_GRPC_MAX_CONNS_PER_IP = 64` is admitted to be undecidable from this repo**, and is set
  generously with `0` to disable, per the instruction to prefer the conservative and reversible
  option where a fork is genuinely unresolvable. The NAT failure mode is documented in README with
  its symptom and its fix rather than being discovered in production.
- **D10. Refusals are reported as a periodic summary, never one line per refusal.** Section 6.5.
  Would have escalated as an observability design decision. Called: summary, because a per-refusal
  line is a new unbounded attacker-driven log site inside the slice whose purpose is to bound
  attacker-driven volume.
- **D11. The gRPC server construction moves out of `main` into `cmd/relay-server/grpc_config.go`.**
  Necessary for any of section 8 to exist, and it follows `watchdog_config.go`'s established shape in
  the same package rather than inventing one.

---

## 12. Acceptance criteria

Mapped to the item's five, with the two amendments called out.

1. `grpc.MaxConcurrentStreams(1)` is set, with a comment stating that `AgentService` has exactly one
   RPC and that an agent opens exactly one stream per connection, proven by a test in which a second
   concurrent stream on one connection blocks until its deadline (test 1), RED at HEAD.
2. A guard test fails if `AgentService` ever gains a second method or stream, with a failure message
   naming the constant to raise (test 2).
3. `grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 5 * time.Minute,
   PermitWithoutStream: false})` is set, with a comment deriving 5 minutes from grpc-go's own
   `defaultKeepalivePolicyMinTime` and from the fact that the agent configures no client keepalive,
   and stating that lowering it is the only way to make it matter and is a regression.
   **Amends the item's criterion 2:** no RED-at-HEAD test is possible, because the behaviour already
   exists. Two characterization tests ship, one of which (test 15) is killed by lowering `MinTime`.
4. A per-peer connection cap exists in-process at the listener, keyed on the TCP source IP, proven by
   an end-to-end test in which a third connection from one source against a cap of two is refused
   (test 11), RED at HEAD, plus unit tests killed by the section 8.6 mutations - in particular the
   `host:port` keying mutation and the map-growth mutation.
5. A total connection cap exists, is `0`-disablable, and is named in the startup line.
6. `MaxConnectionIdle` is set and `0`-disablable, proven by a test that an idle streamless connection
   is closed (test 12, RED at HEAD) **and** a test that a connection holding a silent stream is not
   (test 13).
7. `Accept` never returns an error for an over-limit peer, and a real underlying listener error still
   propagates (tests 10, 11).
8. Refusals are surfaced as at most one log line per reporting interval, containing counts and no
   caller-supplied bytes, proven by test 19.
9. An unconditional startup line names the effective stream cap, total cap, per-IP cap and idle
   timeout, and says explicitly when any of them is disabled. `0`, negative and unparseable values
   behave exactly as `parseWatchdogDuration` defines (tests 17, 18).
10. README documents the three new variables, the NAT hazard for the per-IP cap with its symptom and
    fix, and the note that an operator fronting `:9090` with a proxy may set the caps there and `0`
    here.
11. **Closing the item's criterion 4 by written decision:** README's auto-enroll trust-model
    paragraph states, in the item's own terms, that a reachable host under
    `RELAY_ALLOW_AUTO_ENROLL=true` may create one persistent `workers` row per distinct hostname it
    claims, that those rows survive the connection and a restart and appear in every
    `GET /v1/workers` page, and that this slice bounds the concurrency of that creation and not its
    total.
12. **The item's criterion 5:** `ingestLogLimiter`'s doc comment cites the connection bound by name,
    does the fleet-wide and per-source arithmetic out loud, and says that raising
    `RELAY_GRPC_MAX_CONNS` scales the fleet-wide log budget linearly. Comment only, zero behaviour
    change.
13. Two backlog items are proposed for filing: the revoked-credential connection lifetime
    (unifying the gRPC and SSE halves) and the auto-enroll row bound (related to, not duplicating,
    the CIDR allowlist item).
14. `make test` is green, the `internal/worker` integration suite is unchanged and green, no existing
    test's assertions move, and no SQL, migration, proto or generated file is touched.
