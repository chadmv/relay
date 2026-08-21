# Bound gRPC connection and stream admission - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **THIS PLAN IS THREE FILES. The tasks are NOT in this one.**
> 1. `2026-08-20-grpc-admission-bounds.md` (this file) - goal, refutations, file structure, task index.
> 2. `2026-08-20-grpc-admission-bounds-tasks-1-6.md` - Tasks 1-6.
> 3. `2026-08-20-grpc-admission-bounds-tasks-7-12.md` - Tasks 7-12, mutation matrix, residual risk, backlog items to file.
>
> (Split only because the plan doc could not be written in one pass; treat the three as one document and read them in order.)

**Goal:** Give `:9090` an in-process admission bound - one stream per connection, a total and a per-source-IP connection cap at the listener, and an idle-transport reaper - so that every per-connection control relay already ships (`ingestLogLimiter` above all) is stated per a unit that is itself bounded.

**Architecture:** A new `internal/netlimit` package wraps the gRPC `net.Listener`. Its `Accept` counts live connections in total and per source IP, and refuses an over-limit peer by **accepting and immediately closing** it, then looping - never by returning an error, which `grpc.Server.Serve` would treat as fatal. Release happens through a wrapping `net.Conn` whose `Close` decrements exactly once. A new `cmd/relay-server/grpc_config.go` (mirroring `watchdog_config.go`) owns the `grpc.NewServer` option list, the three env parsers, the unconditional startup bounds line, and a once-per-minute refusal summary.

**Tech Stack:** Go 1.26, `google.golang.org/grpc v1.80.0`, testify. No SQL, no proto, no migration, no generated code, no frontend.

---

## Slice independence declaration

- **BACKEND ONLY. There is ZERO frontend work in this slice.** Nothing under `web/` changes; no REST endpoint, no DTO and no SSE event is added or altered. **Do not dispatch `relay-frontend-engineer` in Phase 3.**
- **One PR, one session, one engineer.** Tasks 1-3 (`internal/netlimit`) and Tasks 4-8 (`cmd/relay-server/grpc_config.go`) touch disjoint files and are independent of each other; Task 9 joins them and Tasks 10-12 follow it. The whole slice is small. **Dispatch one `relay-backend-engineer` and run the tasks in order.**
- **No `Stage N` sections. Nothing here belongs in the backlog as scheduled work.** Three follow-up items are *proposed for filing* (part 3); the conductor files them.
- **No `make generate` step exists in this plan.** If you think you need one, you are off-plan. No `.sql`, no `.proto`, no `*.sql.go`, no `models.go`.

---

## Verification of the spec against HEAD (`4b97895`) and against `grpc@v1.80.0`

The spec is a proposal, not a contract. Everything load-bearing was re-read in the tree and in the module cache at `C:/Users/chadv/go/pkg/mod/google.golang.org/grpc@v1.80.0`. **Confirmed** claims are listed compactly. **Refuted** claims changed this plan and are spelled out, because each would have produced a hung test, a vacuous test, or a green test that checks nothing.

### Confirmed

| Spec claim | Evidence |
|---|---|
| `grpc.NewServer` has exactly one option, and it is the only server in the tree | `cmd/relay-server/main.go:186-191`. A repo-wide grep for `grpc.NewServer\|grpc.NewClient\|grpc.Dial` returns exactly two production hits (`main.go:186`, `agent.go:202`) plus four in `internal/agent`'s own tests, which build optionless servers and are unaffected by this slice. |
| HTTP and gRPC are separate listeners on separate ports | `main.go:193` vs `main.go:238`. No multiplexing. |
| `AgentService` has exactly one RPC and zero unary methods | `internal/proto/relayv1/relay_grpc.pb.go:102-115`: `Methods: []grpc.MethodDesc{}`, one `Streams` entry named `Connect`. |
| `maxConcurrentStreams` defaults to `math.MaxUint32` | `grpc@v1.80.0/server.go:189-190`. |
| The server only advertises `SETTINGS_MAX_CONCURRENT_STREAMS` when it is not the default, so a HEAD client falls back to 100 | `internal/transport/http2_server.go:178-183`; `defaultMaxStreamsClient = 100` at `internal/transport/defaults.go:34`. |
| A compliant grpc-go client **blocks** on stream quota rather than erroring, with `ctx.Done()` as the only escape | `internal/transport/http2_client.go:829-836` and `:885-908`. **A raw "expect an error" assertion really would hang.** |
| The server-side refusal is a transport-level `RST_STREAM(REFUSED_STREAM)` that never reaches the handler, so `newHandlerQuota.acquire()` cannot stall the reader | `http2_server.go:537-547` runs before `t.activeStreams[streamID] = s` at `:582` and before `handle(s)`; `server.go:1058-1063` is only reached for admitted streams. |
| `MaxConnectionIdle` / `MaxConnectionAge` / `MaxConnectionAgeGrace` are all `infinity` by default | `defaults.go:35-37`. |
| A connection holding a stream is never idle | `http2_server.go:266`, `:582-585`, `:1299-1306`, `:1204-1220`. |
| grpc-go already enforces `MinTime = 5m` and `PermitWithoutStream = false`, and a no-stream ping is measured against a 2 hour floor | `http2_server.go:241-244`, `defaults.go:40`, `http2_server.go:863-914`. Nothing here can be tightened. |
| The agent sets no client keepalive at all | `internal/agent/agent.go:196-202` is the complete option list; `defaultClientKeepaliveTime = infinity` (`defaults.go:32`). |
| `Serve` treats a non-`Temporary()` `Accept` error as **fatal** - it returns and the deferred `ls.Close()` kills the listener | `grpc@v1.80.0/server.go:919-952`. Quoted in full under Lead 2. |
| `ingestLogLimiter` is allocated once per `Connect` call, i.e. once per stream | `internal/worker/handler.go:172`. |
| `parseWatchdogDuration` exists with the shape the spec describes | `cmd/relay-server/watchdog_config.go:41-64`; table test at `watchdog_config_test.go:63-96`. |
| `Registry.Register` overwrites, so one valid agent token buys the full multiplication, and a per-worker cap of 1 would break reconnect overlap | `internal/worker/registry.go:27-31`, `UnregisterIf` at `:38-46`. |
| The agent's reconnect is strictly sequential and requeues nothing inside the grace window | `agent.go:97-133`, `:184-212`, `handler.go:380-382`, `main.go:117-122`. |
| Only `trailing_log_window_test.go` and `watchdog_config_test.go` are untagged in `cmd/relay-server`; `bootstrap_test.go` and `startup_reconcile_test.go` are `//go:build integration` | Grep for `go:build` in that directory. **Every test file this plan creates must have NO build tag**, or it silently leaves `make test`. |

### REFUTED - each of these changed the plan

**R1. The spec's keepalive characterization tests (its tests 14, 15 and 16) CANNOT BE WRITTEN. `grpc.WithKeepaliveParams` clamps the client ping interval to a 10-second minimum, and the knob that lowers it is in an internal package relay cannot import.**

`grpc@v1.80.0/dialoptions.go:561-565`:

```go
func WithKeepaliveParams(kp keepalive.ClientParameters) DialOption {
	if kp.Time < internal.KeepaliveMinPingTime {
		logger.Warningf("Adjusting keepalive ping interval to minimum period of %v", internal.KeepaliveMinPingTime)
		kp.Time = internal.KeepaliveMinPingTime
	}
```

`internal/internal.go:40-42`: `KeepaliveMinPingTime = 10 * time.Second`. grpc-go's own ping-policy tests lower it (`test/goaway_test.go:127-129` sets it to `time.Millisecond`) precisely because they live *inside* the module. `google.golang.org/grpc/internal` is not importable from `relay`.

So the spec's `keepalive.ClientParameters{Time: 100 * time.Millisecond}` silently becomes `10s`. Worse, `t.lastPingAt` starts at the zero value so the first ping never strikes (`http2_server.go:901`/`:906`), and `maxPingStrikes = 2` puts the GOAWAY on the **fourth** ping - ~40 seconds per test, two tests, in a package bounded by `make test`'s `-timeout 120s`. And the realistic future regression the spec wanted to catch, somebody writing `MinTime: 10 * time.Second`, is *exactly* the clamp value, so the comparison against a client pinging every 10s is a coin flip. A flaky 40-second test is worse than no test.

**Consequence:** tests 14, 15 and 16 are deleted. The `KeepaliveEnforcementPolicy` still ships (the spec's reasoning for shipping a no-op is sound and unaffected), guarded only by a **constant-lockstep test on relay's own constructed value**, labelled in its own comment as proving nothing about grpc-go. Mutation row 12 is deleted and replaced. Hand-rolling a pinging client on `x/net/http2` is rejected for the same reason the spec rejects it in its section 8.1.

**R2. Mutation matrix row 3, "Add a second RPC to `AgentService`", is not runnable.** It means editing `relay.proto` and running `buf generate`, which this slice forbids. A mutation that cannot be run is a claim, not a kill. **Fix:** the guard test's predicate becomes a named function exercised against *synthetic* `grpc.ServiceDesc` values in the same test; the real descriptor is checked once.

**R3. Mutation matrix row 7, "Return an `error` from `Accept` instead of closing and looping", does NOT redden the spec's test 11 as written.** Under that mutation the whole listener dies, so the third connection is *also* refused and a test that only checks "the third connection is refused" still passes. This is the "poisoned input placed last" lesson in a different costume: the mutation makes the system *more* refusing. **Fix:** Task 9's end-to-end test additionally (a) captures `Serve`'s return in a channel and asserts it has not fired, and (b) after releasing a slot, admits a **fourth** connection and opens a real stream on it.

**R4. Mutation matrix row 11, "Give `MaxConnectionIdle` age semantics", is not compilable as stated** - those semantics live in grpc-go. **Fix:** the runnable equivalent is `MaxConnectionAge: <the idle value>, MaxConnectionAgeGrace: 10 * time.Millisecond` in the same `keepalive.ServerParameters` literal. Note `http2_server.go:226` adds up to +10% jitter to `MaxConnectionAge`, which is why the test holds for 2s against a 200ms value.

**R5. Mutation matrix row 13, "Log one line per refusal instead of a periodic summary", does NOT redden the spec's test 19.** That test drives the *reporter*, which under the mutation still behaves perfectly; the mutation adds a second, unbounded log site inside `netlimit.Accept`. **Fix:** a new test lives where the refusal happens - `TestLimitListener_RefusalWritesNothingToTheLog` redirects the standard logger to a buffer, refuses 100 connections, and asserts the buffer is empty.

**R6. The spec's test 11 does not prove the caps are wired, which is the one thing it claims to prove.** Spec section 8.2 says it exists "so the caps are proven **wired** and not merely implemented" - but it builds its own listener and its own server, so deleting `netlimit.Wrap` from `main.go` leaves it green. This is exactly what `TestWatchdogIsStartedByMain` (`watchdog_config_test.go:129-237`) exists for. **Fix:** Task 9 adds `TestGRPCAdmissionIsWiredByMain`, a `go/ast` guard requiring (i) the identifier passed to `Serve(...)` to trace back to `netlimit.Wrap`, (ii) `grpc.NewServer`'s arguments to include `grpcServerOptions`, and (iii) a `go runRefusalReporter(...)` statement that is a **direct** child of a function body. `go/ast`, not a regex - a regex guard in this repo was proven breakable by one stray comment.

**R7. NEW, not in the spec, and it is a real behavioural regression this slice introduces: wrapping the accepted `net.Conn` silently disables `TCP_USER_TIMEOUT` on Linux.** `NewServerTransport` does `rawConn := conn` (`http2_server.go:151`) and, because relay sets `Time: 30s` which is not `infinity`, calls `syscall.SetTCPUserTimeout(rawConn, kp.Timeout)` (`:236-240`). That function is:

```go
func SetTCPUserTimeout(conn net.Conn, timeout time.Duration) error {
	tcpconn, ok := conn.(*net.TCPConn)
	if !ok {
		// not a TCP connection. exit early
		return nil
	}
```

(`internal/syscall/syscall_linux.go:71-76`). The assertion is on the **concrete type**, so no interface trick on the wrapper can satisfy it, and it fails silently by returning `nil`. Relay gets this option today and will not after this slice.

**Severity: low and bounded, but it must be recorded rather than discovered.** The application-layer equivalent is unaffected: `http2Server.keepalive()` (`:1234-1263`) decides liveness from `atomic.LoadInt64(&t.lastRead)`, not from whether a write succeeded, so a dead peer is still torn down at `Time + Timeout` = 40s. `TCP_USER_TIMEOUT` was defence in depth against a write parked in the kernel send buffer. **Decision: accept the loss**, state it in `netlimit`'s doc comment, and file an item. Restoring it means a `//go:build linux` file duplicating a grpc-go internal with a timeout plumbed from `cmd/relay-server`, plus a platform-gated test this repo has a standing lesson about verifying in Docker. That is a slice of its own.

**R8. The spec's section 6.4 argument for `MaxConnectionIdle` is right about the conclusion and imprecise about the mechanism, and the imprecision changes what the tests may claim.** `t.idle` is stamped only once the transport exists (`http2_server.go:266`), i.e. *after* the HTTP/2 preface. A connection that completes TCP and then says nothing never reaches that point - it is parked inside `io.ReadFull(t.conn, preface)` under the deadline `handleRawConn` set at `server.go:974`, bounded by `connectionTimeout`, default **120 seconds** (`server.go:193`). So a mute peer holds a slot for 120s (bounded by grpc-go, not by us), while a peer that speaks the preface and opens no stream holds one forever at HEAD - and *that* is what `MaxConnectionIdle` closes. Both go in the option's comment. Task 9's end-to-end test uses mute raw `net.Dial` connections, so it exercises the first case and must not be described as exercising the second.

**R9. `0` disabling `RELAY_GRPC_MAX_CONN_IDLE` requires no relay-side branch, and the plan must not add one.** `http2_server.go:219-221` maps a zero `MaxConnectionIdle` to `infinity`. An `if idle > 0 { ... }` wrapper would be dead code a reader would take for a decision.

**R10. There is exactly ONE `grpc.KeepaliveParams` option and it must stay that way.** `MaxConnectionIdle` lives in the same `keepalive.ServerParameters` struct as the existing `Time: 30s` / `Timeout: 10s` probe. Appending a *second* `grpc.KeepaliveParams(...)` compiles, is the obvious way to write this diff, and silently discards `Time` and `Timeout` because the later option overwrites `o.keepaliveParams` wholesale (`server.go:330-332`). Task 4 factors the struct into `grpcKeepaliveParams(idle)` and pins all three fields. The spec does not mention this.

### Settled, not refuted: the seven conductor leads

**Lead 1 - is `MaxConcurrentStreams(1)` actively dangerous? NO, and the evidence is in the agent's own connect path.** The hazard needs a client that reuses one `ClientConn` across reconnects. relay's agent does not: `connect` calls `grpc.NewClient(a.coord, opts...)` and `defer conn.Close()` on **every attempt** (`internal/agent/agent.go:202-206`), then opens exactly one stream (`:209`). `Run` (`:97-133`) calls `connect` strictly sequentially, and `connect` waits on `a.sendWG.Wait()` (`:194`) first. There is never a second stream, nor a second connection, per agent process. With `AgentService` carrying zero unary methods and one stream, "one stream per connection" is a property of the wire contract. **`1` stands, with no headroom**, and the brittleness is bought off by the guard test restructured per R2.

Two secondary facts checked while settling this, both benign, both recorded so nobody re-derives them:

- *When does the server release stream quota relative to the handler returning?* Two counters. The transport's `t.activeStreams` entry - the one the `>= t.maxStreams` refusal reads at `:537` - is removed in `deleteStream` (`:1299-1307`) when the stream closes. `server.go`'s separate `newHandlerQuota` semaphore is released by `defer streamQuota.release()` (`:1063`) after `handleStream` returns, slightly *later*. A client that pipelines new HEADERS into that gap is admitted by the transport and then parks the reader goroutine in `acquire()` until the old handler's release runs. Transient, self-clearing, cannot deadlock (it is a `defer`), and relay's agent never pipelines.
- *Could headroom be bought back by moving where the limiter is allocated, as the lead hints?* In principle yes - connection-scoped state would decouple the budget from the stream cap. It is also a direct attack on `handler.go:161-171`, which argues at length that being a `Connect` stack local is what makes the limiter mutex-free and teardown-free. **Out of scope, deliberately.**

**Lead 2 - does the listener wrapper bound what the spec claims, and what must refusal return?** Settled; the spec is right for the right reason. `grpc@v1.80.0/server.go:919-952`:

```go
	var tempDelay time.Duration // how long to sleep on accept failure
	for {
		rawConn, err := lis.Accept()
		if err != nil {
			if ne, ok := err.(interface {
				Temporary() bool
			}); ok && ne.Temporary() {
				...
				continue
			}
			s.mu.Lock()
			s.printf("done serving; Accept = %v", err)
			s.mu.Unlock()

			if s.quit.HasFired() {
				return nil
			}
			return err
		}
```

A returned error that is not `Temporary()` ends `Serve`, and its deferred block at `:907-914` calls `ls.Close()`. **An admission control expressed as an `Accept` error is a self-DoS.** A `Temporary()` error is also wrong: `Serve` retries those with a 5ms-to-1s backoff, rate-limiting every honest peer queued behind the abusive one. **Accept-then-close-then-loop it is.** The consequence the spec does not draw out: because refusal never surfaces at `Accept`, no unit test can observe it in a return value. Every refusal test is therefore shaped as "the refused conn was `Close`d **and** the next admissible conn was still returned" - the poisoned input placed **first**, with a good one after it. All of Task 1's tests are built that way.

**Lead 3 - connection release, every close path enumerated.** The wrapper's `Close` is the only hook and it is sufficient, because grpc-go never unwraps: the value returned from `Accept` is stored as `t.conn` (`http2_server.go:254`) and every close path goes through it.

| # | Path | Closes | Reaches our wrapper? |
|---|---|---|---|
| 1 | `handleRawConn` when `Stop`/`GracefulStop` already fired | `rawConn.Close()` (`server.go:970-973`) | yes |
| 2 | `newHTTP2Transport` on any `NewServerTransport` error | `c.Close()` (`server.go:1027-1033`) | yes (the `ErrConnDispatched` exemption is unreachable: relay sets no `grpc.Creds`) |
| 3 | `NewServerTransport`'s deferred `t.Close(err)` (`http2_server.go:303-307`) | `t.conn.Close()` (`:1288`) | yes |
| 4 | `NewServerTransport` failing **before** that defer is registered - `WriteSettings` (`:209`), `WriteWindowUpdate` (`:214`), `SetTCPUserTimeout` (`:237`) | nothing there; falls through to path 2 | yes, via 2 |
| 5 | loopy writer exiting on a non-I/O error | `t.conn.Close()` (`:340-361`) | yes |
| 6 | `serveStreams`' deferred `st.Close(...)` - the ordinary end of a connection | `t.conn.Close()` (`:1288`) | yes |
| 7 | `Server.Stop` / `GracefulStop` iterating `s.conns` | `st.Close(...)` -> `:1288` | yes |

**Paths 2 and 3 fire together on the commonest failure of all** - a peer that opens TCP and hangs up before the preface. `io.ReadFull` returns `io.EOF`, `NewServerTransport` returns `(nil, io.EOF)` which assigns the *named* return, so the defer runs `t.Close(io.EOF)` -> `conn.Close()`; then `newHTTP2Transport` runs `c.Close()` on the same conn. **Double `Close` is the normal case, not an edge case.** `sync.Once` is load-bearing, and the failure without it is not a leak but its mirror: over-release, so the counter drifts and the cap stops firing. Both directions are tested in Task 2; the mutation is "delete the `sync.Once`". Nothing closes the conn behind our back, and the only type assertions on it are `SetTCPUserTimeout` (R7) and `channelz.GetSocketOption` (metrics only). `rawConn.SetDeadline` at `server.go:974` reaches the real socket through struct embedding.

**Lead 4 - the 13-row mutation matrix.** Four rows fail "compilable and behaviourally detectable": row 3 (R2), row 7 (R3), row 11 (R4), row 13 (R5). Row 12 is void because its test cannot exist (R1). The corrected 19-row matrix is in Task 12, and it requires a **green-baseline run before the battery starts**, a re-baseline after each revert, and a kill confirmed **by failing-test name** per row - because uniform results mean a broken harness, not good coverage, and a compile error is recorded as `NOT RUN`, never as a kill.

**Lead 5 - hang risk and the `MaxConnectionIdle` seam.** Every real-server test carries an explicit bound and none waits on a default. The seam exists because Task 4 creates it: `grpcServerOptions(b grpcBounds) []grpc.ServerOption` takes a plain struct, so a test passes `grpcBounds{maxConnIdle: 200 * time.Millisecond}` directly. **No env var is read in any test and no test-only global is added.** Bounds: the second-stream test's 2s `context.WithTimeout` *is* its assertion; the idle tests use `select` with `time.After(5*time.Second)` and a fixed 2s sleep; the end-to-end test uses `SetReadDeadline` and `require.Eventually` capped at 5s. `make test`'s `-timeout 120s` is the per-package backstop; the new tests add well under 15s to `cmd/relay-server`.

**Lead 6 - env plumbing.** `parseWatchdogDuration` exists at `watchdog_config.go:41-64` and its shape is followed exactly. It is **not reused directly**: its `d == 0` message is watchdog-specific prose, and generalising it would move assertions in `watchdog_config_test.go:63-96`, which spec criterion 14 forbids. Tasks 6-7 add two siblings with the same contract and their own strings. One deliberate deviation, written into the code: **the two integer knobs get no `floor` outcome**, because a floor catches units confusion and a bare connection count has no units; the duration knob keeps a floor (1s) because it does have a fail-aggressive direction. README rows are Task 11 and are in scope.

**Lead 7 - scope.** One PR. The exits are honest, checked bullet by bullet against the item's Acceptance list (`docs/backlog/bug-2026-08-15-grpc-connection-admission-is-unbounded.md:106-118`), which has five bullets and does **not** mention `MaxConnectionAge` - that appears only under Proposal. Bullet 3 offers "either a per-peer connection cap exists, **or** the proxy decision is written into README"; we ship the cap. Bullet 4 offers "auto-enroll row creation is bounded, **or** the decision to leave it unbounded is written into the auto-enroll trust model in README, in the same terms this item uses"; Task 11 writes exactly that. **One criterion is genuinely amended rather than met:** bullet 2 asks for `MinTime` "derived from the agent's configured keepalive" and "a test that a legitimate agent's cadence is not terminated". The agent has no configured keepalive, so there is nothing to derive from and no cadence to protect, and per R1 there is not even a characterization test. What ships is the value, the derivation in the comment, and a constant-lockstep test. **The conductor should record bullet 2 as closed by amendment, not by satisfaction.**

**Invariants.** *One bounded sender per gRPC stream*: untouched, and `MaxConcurrentStreams(1)` strengthens its premise by making "one stream per connection" enforced rather than conventional. *Identity-checked teardown*: untouched, and there is no interaction - a refused connection never registers, so no `workerSender` exists, no grace timer is armed (`handler.go:148` arms teardown only *after* `authenticateAndRegister` succeeds) and nothing requeues. The limiter holds no worker identity; its release key is a string and its teardown is per-conn and idempotent. *End the generation before releasing the resource*: the wrapper decrements **after** `c.Conn.Close()` returns, so a slot is never handed out while its predecessor's FD is open. *No interior pointers across locks*: the per-IP map is guarded by one mutex and never yields a pointer. *Epoch fence*, *Single job-spec pipeline*, *Single JSON entry point*: not applicable.

**One reconnect hazard the spec does not name, and README must:** because a reconnecting agent's new TCP connection can arrive before the server has processed the old one's FIN, `RELAY_GRPC_MAX_CONNS_PER_IP=1` is a footgun. It happens to work - `connect`'s `defer conn.Close()` (`agent.go:206`) runs before `reconnectWait` sleeps at least 1s (`agent.go:126`, `initialReconnectBackoff` at `:68`) - but with no margin. No floor is added (that would invent a fourth parsing convention); the README row says not to set it to 1, and why.

---

## File structure

**Create**

| File | Responsibility |
|---|---|
| `internal/netlimit/listener.go` | `Config`, `Stats`, `Listener` (a `net.Listener`), the accounting `conn` wrapper. The whole package. |
| `internal/netlimit/listener_test.go` | White-box (`package netlimit`) unit tests + the fake listener/conn. No gRPC, no network. |
| `cmd/relay-server/grpc_config.go` | `grpcMaxConcurrentStreams`, `grpcBounds`, `grpcServerOptions`, `grpcKeepaliveParams`, `grpcEnforcementPolicy`, `parseConnLimit`, `parseGRPCConnIdle`, `grpcBoundsLine`, `refusalReporter`, `runRefusalReporter`. Mirrors `watchdog_config.go`. |
| `cmd/relay-server/grpc_config_test.go` | Pure-function tests: the service-desc guard, the keepalive lockstep, env parsing, the bounds line, the reporter, and the `go/ast` wiring guard. **No build tag.** |
| `cmd/relay-server/grpc_server_test.go` | Real-server tests: stream cap, idle reaping, end-to-end connection refusal. Stub `AgentServiceServer`, no database. **No build tag.** |

**Modify**

| File | What |
|---|---|
| `cmd/relay-server/main.go:185-202` | Parse three env vars, log `grpcBoundsLine`, build the server from `grpcServerOptions`, wrap the listener with `netlimit.Wrap`, start `runRefusalReporter`. Drops the now-unused `keepalive` import. |
| `internal/worker/ingest_log_limiter.go:1-37` | Doc-comment paragraph citing the connection bound and doing the arithmetic. **Comment only, zero behaviour change.** |
| `README.md:277-286` (env table), `:306-311` (startup sequence), `:360-364` (auto-enroll trust model) | Three rows, the NAT hazard, the proxy note, the auto-enroll sentence. |

**Critical files - read these before writing anything:** `cmd/relay-server/watchdog_config.go` (the parser and bounds-line shape you are copying), `cmd/relay-server/watchdog_config_test.go:98-237` (the AST guard you are copying), `cmd/relay-server/main.go:185-202`, `internal/worker/ingest_log_limiter.go`, and CLAUDE.md's Invariants section.

**Never touched:** anything under `web/`, `internal/store/`, `proto/`, `internal/proto/`, `internal/agent/`, `internal/worker/handler.go`.

---

## Task index

Tasks 1-6 are in `2026-08-20-grpc-admission-bounds-tasks-1-6.md`; Tasks 7-12 in `2026-08-20-grpc-admission-bounds-tasks-7-12.md`.

| # | Task | Depends on |
|---|---|---|
| 1 | `internal/netlimit` refuses an over-limit source IP without erroring | - |
| 2 | Closing a connection releases its slot exactly once, and the map does not grow | 1 |
| 3 | The total cap, zero-disables, and no log line on the refusal path | 1 |
| 4 | `MaxConcurrentStreams(1)` and the keepalive options, behind a testable option builder | - (independent of 1-3) |
| 5 | `MaxConnectionIdle` reaps a streamless transport and never a busy one | 4 |
| 6 | Env parsing for the three knobs | 4 |
| 7 | The unconditional startup bounds line | 6 |
| 8 | Refusals surface as a periodic summary, never one line per refusal | 3, 4 |
| 9 | Wire it into `main.go`, and prove the wiring | 1-8 |
| 10 | `ingestLogLimiter`'s doc comment cites the bound and does the arithmetic | 9 |
| 11 | README - three env rows, the NAT hazard, the proxy note, the auto-enroll decision | 9 |
| 12 | Gates, and the mutation battery | all |

**Task 9 is the one the slice depends on. Everything before it compiles, passes, and bounds nothing in production.**
