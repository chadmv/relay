# Design: One wedged agent can halt task dispatch for the entire farm

- Date: 2026-06-10
- Backlog item: `docs/backlog/bug-2026-06-10-wedged-agent-halts-dispatch.md`
- Status: approved, ready for implementation plan

## Problem

Two independent failure modes compound into a farm-wide outage:

1. `workerSender.Send` blocks until buffer space frees or the sender closes
   (`internal/worker/sender.go:52-65`). It only blocks on `queue <- message`
   when the 64-slot buffer is full.
2. No gRPC keepalive is configured on the server (`cmd/relay-server/main.go:176`),
   so a half-open or wedged stream is never detected. The send goroutine stays
   blocked in `stream.Send` on gRPC flow control indefinitely, the buffer fills,
   and the next `Send` blocks forever.

Blast radius: the dispatch loop is a single goroutine
(`internal/scheduler/dispatch.go:120-128`), so one stuck `Send` freezes dispatch
for the entire farm. Three HTTP handlers also call `Registry.Send` with no
timeout and hang: job cancel (`internal/api/jobs.go:755`), worker disable
(`internal/api/workers.go:502`), workspace evict (`internal/api/workspaces.go:79`).

## Decisions

Resolved during brainstorming:

- **Recovery: bound + keepalive only.** Bound the send so no single call blocks
  forever, and add server keepalive so wedged streams die on their own. No
  proactive eviction of the wedged worker and no dispatcher cooldown - rejected
  as added complexity for a worst case of a few wasted ~5s timeouts during the
  keepalive detection window.
- **Keepalive scope: server-only.** Agent-side (client) keepalive is out of
  scope; agent reconnect behavior is tracked separately in
  `bug-2026-06-10-reconnect-backoff-never-resets`.
- **Timeout value: hardcoded.** A package var `sendTimeout = 5 * time.Second`
  (var, not const, so tests can lower it). No env var.

## Change 1: Bounded send in `workerSender`

File: `internal/worker/sender.go`.

Add a sentinel error and a tunable timeout:

```go
var ErrSendTimeout = errors.New("worker send timed out")

// package var (not const) so tests can lower it
var sendTimeout = 5 * time.Second
```

Rename the cramped struct fields for readability (file-wide; rename only, no
behavior change in `loop()`/`Close()`):

```go
type workerSender struct {
	stream  Sender
	queue   chan *relayv1.CoordinatorMessage
	stopReq chan struct{}
	closed  chan struct{}
	once    sync.Once
}
```

- `ch`   -> `queue`
- `stop` -> `stopReq`
- `done` -> `closed`
- receiver `ws` -> `sender`

Add the timeout to the blocking enqueue in `Send`:

```go
func (sender *workerSender) Send(message *relayv1.CoordinatorMessage) error {
	select {
	case <-sender.closed:
		return ErrWorkerDisconnected
	default:
	}
	timeout := time.NewTimer(sendTimeout)
	defer timeout.Stop()
	select {
	case sender.queue <- message:
		return nil
	case <-sender.closed:
		return ErrWorkerDisconnected
	case <-timeout.C:
		return ErrSendTimeout
	}
}
```

`loop()` and `Close()` get the same field renames and are otherwise unchanged.

### Why a timeout is safe (no duplicate dispatch)

`Send` only blocks on `queue <- message` when the buffer is full, which means
the send goroutine is itself parked in a blocked `stream.Send`. A timeout
therefore means the message was **never enqueued** - it cannot be delivered
later as a duplicate. The dispatcher receives `ErrSendTimeout`, requeues the
task, and another worker can safely take it.

The already-buffered messages (for which `Send` previously returned `nil`)
remain marked `dispatched` in the DB. They are cleaned up by the normal
disconnect -> grace -> requeue teardown once keepalive kills the stream (that
requeue path bumps `assignment_epoch`, so any late delivery to the old agent is
fenced out).

## Change 2: Server-side gRPC keepalive

File: `cmd/relay-server/main.go` (currently `grpc.NewServer()` at line 176).

```go
grpcSrv := grpc.NewServer(
	grpc.KeepaliveParams(keepalive.ServerParameters{
		Time:    30 * time.Second, // ping after 30s of transport inactivity
		Timeout: 10 * time.Second, // close if no ack within 10s
	}),
)
```

Adds import `"google.golang.org/grpc/keepalive"`.

When an agent connection wedges, the server's keepalive ping goes unacked and
gRPC closes the transport after ~`Time + Timeout` (~40s). The close unblocks the
send goroutine stuck in `stream.Send`; the goroutine returns, `closed` is
signaled, and the `Connect` handler's `Recv` errors, triggering the normal
disconnect -> grace -> requeue teardown.

Notes:

- No `KeepaliveEnforcementPolicy`: that governs client-initiated pings, and the
  agent does not send keepalives in this work, so the default is fine.
- This same keepalive config is also required by
  `bug-2026-06-10-stale-stream-teardown-clobbers-registration`. It is shared:
  whichever item lands first adds it, and the other must not double-add it.

## Change 3: Call-site behavior and observability

**Dispatcher** (`internal/scheduler/dispatch.go:262`). The existing requeue-on-
error branch handles `ErrSendTimeout` unchanged. Add one log line so a wedged
worker is diagnosable instead of silent:

```go
if err := d.registry.Send(uuidStr(w.ID), msg); err != nil {
	log.Printf("dispatch: send to worker %s failed: %v; requeueing task %s", uuidStr(w.ID), err, claimed.ID)
	_ = d.q.RequeueTask(ctx, claimed.ID)
	return false
}
```

**HTTP handlers** (`jobs.go:755`, `workers.go:502`, `workspaces.go:79`). No
change. They already discard the send error (`_ = s.registry.Send(...)`); the
fix is that each `Send` now returns within `sendTimeout` (~5s) instead of
hanging forever. Note the cancel/disable handlers loop over a job's running
tasks synchronously, so a job whose tasks all sit on one wedged worker can take
up to N x ~5s in the request path (bounded, was previously unbounded). This runs
after the DB commit, so state is already durable. Cancel/disable/evict are
best-effort signals and the worker's eventual disconnect handles cleanup.

## Out of scope (deliberate boundaries)

- **Epoch bump in `RequeueTask`.** `RequeueTask` (`internal/store/query/tasks.sql:99`)
  does not bump `assignment_epoch`. This is safe for the timeout path because a
  timed-out send was never enqueued, so no agent holds the assignment. The
  missing bump is tracked separately in
  `bug-2026-06-10-requeue-paths-skip-epoch-bump` and is not changed here.
- **Agent-side keepalive / reconnect** - `bug-2026-06-10-reconnect-backoff-never-resets`.
- **Stale-stream teardown / `UnregisterIf`** - `bug-2026-06-10-stale-stream-teardown-clobbers-registration`
  (shares the keepalive config above).

## Testing

Unit tests in `internal/worker/sender_test.go`, lowering `sendTimeout` via the
package var and using a `Sender` stub whose `Send` blocks on a channel to
simulate a wedged stream:

1. **Times out on a full buffer (core regression).** Stub blocks forever; fill
   the 64-slot buffer (one message parked in the blocked `stream.Send`), then
   assert the next `Send` returns `ErrSendTimeout` within a bounded margin of
   the lowered timeout. Fails against today's code (blocks forever).
2. **Returns `ErrWorkerDisconnected` when closed mid-wait.** Fill the buffer,
   call `Close()` from another goroutine, assert the blocked `Send` returns
   `ErrWorkerDisconnected`.
3. **Fast path unaffected.** With a non-blocking stub, `Send` returns `nil`.

Keepalive: no automated test. `keepalive.ServerParameters` is gRPC-internal
config with no unit-testable behavior, and a real wedged-socket test needs
network-level fault injection that does not fit the unit suite. Manual/
integration check: agent connects, kill the agent's network path ungracefully,
confirm the server tears the worker down within ~40s.

No new dispatcher test: the requeue-on-error path at `dispatch.go:262` is
already covered; `ErrSendTimeout` is another non-nil error through the same
branch, and the added line is a log.

## Success criteria

- `go test ./internal/worker/... ` passes, including the new full-buffer timeout
  test (which fails before Change 1).
- `make build` succeeds with the keepalive import.
- A full buffer no longer blocks `Send` indefinitely; the dispatch loop and the
  three HTTP handlers return within ~`sendTimeout` when a worker is wedged.
