# gRPC admission bounds - Tasks 7-12, mutation matrix, residual risk

> **Part 3 of 3.** Read `2026-08-20-grpc-admission-bounds.md` first, then `2026-08-20-grpc-admission-bounds-tasks-1-6.md`.

---

## Task 7: The unconditional startup bounds line

**Files:** Modify `cmd/relay-server/grpc_config.go` and `cmd/relay-server/grpc_config_test.go`.

- [ ] **Step 1: Write the failing test.** Append to `cmd/relay-server/grpc_config_test.go`:

```go
// TestGRPCBoundsLine mirrors TestWatchdogBoundsLine. A mechanism that can REFUSE
// a user's agent must state its limits at every boot: the ordinary path is
// otherwise completely silent, so an operator cannot tell from the log whether
// the caps are on, what they are, or that somebody switched them off.
func TestGRPCBoundsLine(t *testing.T) {
	all := grpcBoundsLine(grpcBounds{maxConns: 1024, maxConnsPerIP: 64, maxConnIdle: 15 * time.Minute})
	assert.Contains(t, all, "1024")
	assert.Contains(t, all, "64")
	assert.Contains(t, all, "15m")
	assert.Contains(t, all, "1", "the stream cap is part of the admission story and must be named")
	assert.NotContains(t, all, "DISABLED")

	noTotal := grpcBoundsLine(grpcBounds{maxConns: 0, maxConnsPerIP: 64, maxConnIdle: 15 * time.Minute})
	assert.Contains(t, noTotal, "DISABLED")
	assert.Contains(t, noTotal, "64", "one cap off is not both caps off; saying so sends an operator hunting the wrong thing")

	noPerIP := grpcBoundsLine(grpcBounds{maxConns: 1024, maxConnsPerIP: 0, maxConnIdle: 15 * time.Minute})
	assert.Contains(t, noPerIP, "DISABLED")
	assert.Contains(t, noPerIP, "1024")

	noIdle := grpcBoundsLine(grpcBounds{maxConns: 1024, maxConnsPerIP: 64, maxConnIdle: 0})
	assert.Contains(t, noIdle, "DISABLED")
	assert.Contains(t, noIdle, "1024")

	off := grpcBoundsLine(grpcBounds{})
	assert.Equal(t, 3, strings.Count(off, "DISABLED"),
		"all three knobs off is the single most important thing this line can say")
}
```

Add `"strings"` to the imports.

- [ ] **Step 2: Run test to verify it fails.** Run `go test ./cmd/relay-server/ -run TestGRPCBoundsLine -v`. Expected: FAIL to build, `undefined: grpcBoundsLine`.

- [ ] **Step 3: Write minimal implementation.** Append to `cmd/relay-server/grpc_config.go`:

```go
// Defaults for the three knobs.
//
//   - defaultGRPCMaxConns: far above any plausible relay fleet and far below
//     where file descriptors or goroutines hurt. Anchor: RELAY_DB_MAX_CONNS
//     defaults to 25 and an agent's recv loop holds at most one in-flight
//     statement, so more than ~25 simultaneously busy agents already queue on
//     the pool.
//   - defaultGRPCMaxConnsPerIP: NOT DERIVABLE from this repo, and said so out
//     loud. One agent process is strictly one connection at a time
//     (internal/agent/agent.go:184-212), so the legitimate maximum is "how many
//     agent processes share a source address", which depends on NAT topology
//     nothing here can see. 64 is chosen generously and is reversible.
//   - defaultGRPCMaxConnIdle: a legitimate agent's idle window is the gap
//     between dialing and opening its stream, sub-millisecond on a LAN, so this
//     is bounded below only by paranoia about slow middleboxes.
const (
	defaultGRPCMaxConns      = 1024
	defaultGRPCMaxConnsPerIP = 64
	defaultGRPCMaxConnIdle   = 15 * time.Minute
)

// grpcBoundsLine renders the unconditional startup line naming every effective
// admission bound, in the shape of watchdogBoundsLine
// (cmd/relay-server/watchdog_config.go:88). It must say DISABLED explicitly for
// each knob that is off: a disabled safety bound is never allowed to be silent.
func grpcBoundsLine(b grpcBounds) string {
	total := fmt.Sprintf("%d total", b.maxConns)
	if b.maxConns <= 0 {
		total = "total DISABLED"
	}
	perIP := fmt.Sprintf("%d per source IP", b.maxConnsPerIP)
	if b.maxConnsPerIP <= 0 {
		perIP = "per-source-IP DISABLED"
	}
	idle := fmt.Sprintf("idle transports reaped after %s", b.maxConnIdle)
	if b.maxConnIdle <= 0 {
		idle = "idle reaping DISABLED"
	}
	return fmt.Sprintf("gRPC admission: %d stream(s) per connection; connections %s, %s; %s",
		grpcMaxConcurrentStreams, total, perIP, idle)
}
```

- [ ] **Step 4: Run test to verify it passes.** Run `go test ./cmd/relay-server/ -run TestGRPCBoundsLine -v`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/grpc_config.go cmd/relay-server/grpc_config_test.go
git commit -m "feat(server): unconditional startup line naming every gRPC admission bound"
```

---

## Task 8: Refusals surface as a periodic summary, never one line per refusal

**Files:** Modify `cmd/relay-server/grpc_config.go` and `cmd/relay-server/grpc_config_test.go`.

- [ ] **Step 1: Write the failing test.** Append to `cmd/relay-server/grpc_config_test.go`:

```go
// TestRefusalSummaryLogsOnlyWhenCountersMove.
//
// A log.Printf per refused connection would be a new, unbounded,
// ATTACKER-DRIVEN log site on the exact path this slice exists to bound - the
// 2026-08-15 lesson one layer down. The reporter is bounded at one line per
// interval BY CONSTRUCTION, and it carries counts only.
//
// This test is necessary and NOT sufficient: adding a per-refusal line inside
// netlimit leaves this reporter perfectly correct. That half is pinned by
// TestLimitListener_RefusalWritesNothingToTheLog, in the package where the
// refusal happens.
func TestRefusalSummaryLogsOnlyWhenCountersMove(t *testing.T) {
	type line struct {
		format string
		args   []any
	}
	var lines []line
	r := &refusalReporter{logf: func(f string, a ...any) { lines = append(lines, line{f, a}) }}

	r.tick(netlimit.Stats{})
	assert.Empty(t, lines, "a quiet interval must produce no line at all")

	r.tick(netlimit.Stats{RefusedPerIP: 3})
	require.Len(t, lines, 1, "the first movement must be reported")

	r.tick(netlimit.Stats{RefusedPerIP: 3})
	assert.Len(t, lines, 1,
		"an unchanged counter must not re-log: a sustained attack must cost ONE line per interval, not one per tick")

	r.tick(netlimit.Stats{RefusedTotal: 2, RefusedPerIP: 3})
	require.Len(t, lines, 2, "a movement on the OTHER counter must also be reported")

	for i, l := range lines {
		assert.Contains(t, l.format, "%d", "line %d must be a counts template", i)
		require.NotEmpty(t, l.args)
		for _, a := range l.args {
			assert.IsType(t, uint64(0), a,
				"the summary must carry COUNTS ONLY. A caller-supplied byte here (a peer address) would "+
					"make this an attacker-writable log site inside the slice that bounds attacker-driven "+
					"log volume.")
		}
	}
}
```

Add `"relay/internal/netlimit"` to the imports.

- [ ] **Step 2: Run test to verify it fails.** Run `go test ./cmd/relay-server/ -run TestRefusalSummary -v`. Expected: FAIL to build, `undefined: refusalReporter`.

- [ ] **Step 3: Write minimal implementation.** Append to `cmd/relay-server/grpc_config.go` (add `"context"`, `"log"` and `"relay/internal/netlimit"` to its imports):

```go
// grpcRefusalReportInterval is how often the refusal summary may speak. One line
// per minute, and only when something moved.
const grpcRefusalReportInterval = time.Minute

// refusalReporter turns netlimit's counters into at most one log line per
// interval. A line per refusal is deliberately NOT an option: it would be a new
// unbounded attacker-driven log site inside the control that bounds
// attacker-driven log volume. The line names counts and never addresses, so no
// caller-supplied byte can reach the log through it.
//
// tick is separate from runRefusalReporter so the "only when counters move"
// property can be driven directly, with no timer and no sleeping.
type refusalReporter struct {
	last netlimit.Stats
	logf func(format string, args ...any)
}

func (r *refusalReporter) tick(s netlimit.Stats) {
	if s == r.last {
		return
	}
	r.logf("gRPC admission: %d connection(s) refused over the total cap and %d over the per-source-IP cap since startup",
		s.RefusedTotal, s.RefusedPerIP)
	r.last = s
}

// runRefusalReporter logs a refusal summary at most once per interval, in the
// shape of runEnrollmentJanitor (cmd/relay-server/main.go:269).
func runRefusalReporter(ctx context.Context, l *netlimit.Listener, interval time.Duration) {
	r := &refusalReporter{logf: log.Printf}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(l.Stats())
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes.** Run `go test ./cmd/relay-server/ -run TestRefusalSummary -v`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/grpc_config.go cmd/relay-server/grpc_config_test.go
git commit -m "feat(server): periodic gRPC refusal summary, bounded at one line per interval"
```

---

## Task 9: Wire it into `main.go`, and prove the wiring

**Files:** Modify `cmd/relay-server/main.go:185-202`; append to `cmd/relay-server/grpc_config_test.go` and `cmd/relay-server/grpc_server_test.go`.

**This is the task the whole slice depends on. Everything before it compiles, passes and bounds nothing in production.**

- [ ] **Step 1: Write the failing test.** Append to `cmd/relay-server/grpc_config_test.go`:

```go
// TestGRPCAdmissionIsWiredByMain is a structural guard in the same spirit as
// TestWatchdogIsStartedByMain (watchdog_config_test.go:129). Deleting the
// netlimit.Wrap call from main.go compiles and leaves `go test ./...` fully
// green - netlimit keeps its own passing unit tests, grpc_config keeps its own,
// and the agent port silently has no connection bound again, which is the entire
// bug. The end-to-end test next door builds its OWN listener, so it cannot see
// this.
//
// go/ast, NOT a regex: a source-scanning regex guard in this repo was proven
// breakable by a single stray comment.
//
// WHAT IT CANNOT REACH, stated rather than overclaimed: it cannot tell that
// `grpcBnds = grpcBounds{}` inserted just above the Wrap call disables both caps,
// and it cannot tell that runRefusalReporter was handed a pre-cancelled context.
// Both compile and both leave every package green.
func TestGRPCAdmissionIsWiredByMain(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	// name assigned -> identifiers its RHS mentions, so the walk can follow
	// `x := netlimit.Wrap(...)` and then srv.Serve(x).
	from := map[string][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		var rhs []string
		for _, e := range as.Rhs {
			ast.Inspect(e, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					rhs = append(rhs, id.Name)
				}
				return true
			})
		}
		for _, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				from[id.Name] = append(from[id.Name], rhs...)
			}
		}
		return true
	})
	reaches := func(seed, target string) bool {
		seen := map[string]bool{}
		queue := []string{seed}
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			if name == target {
				return true
			}
			queue = append(queue, from[name]...)
		}
		return false
	}
	mentions := func(n ast.Node, want string) bool {
		found := false
		ast.Inspect(n, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Name == want {
				found = true
			}
			return !found
		})
		return found
	}

	// 1. The listener handed to Serve must derive from netlimit.Wrap.
	var serveArg string
	ast.Inspect(file, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Serve" || len(ce.Args) != 1 {
			return true
		}
		if id, ok := ce.Args[0].(*ast.Ident); ok {
			serveArg = id.Name
		}
		return true
	})
	require.NotEmpty(t, serveArg, "main.go has no `<server>.Serve(<listener>)` call with a single identifier argument")
	require.True(t, reaches(serveArg, "Wrap"),
		"the listener passed to grpcSrv.Serve(%s) does not derive from netlimit.Wrap: the gRPC port has NO "+
			"connection cap, in total or per source IP, and nothing else fails", serveArg)

	// 2. grpc.NewServer must be built from grpcServerOptions, or the stream cap,
	//    the keepalive policy and MaxConnectionIdle are all absent.
	var newServer *ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := ce.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewServer" {
			newServer = ce
		}
		return true
	})
	require.NotNil(t, newServer, "main.go no longer calls grpc.NewServer")
	require.True(t, mentions(newServer, "grpcServerOptions"),
		"grpc.NewServer must be built from grpcServerOptions(...): otherwise MaxConcurrentStreams, the "+
			"keepalive enforcement policy and MaxConnectionIdle are all silently absent")

	// 3. The refusal reporter must be started, and NOT from inside a conditional.
	//    A `go` statement nested in an if-body would leave refusals unreported
	//    while an ast.Inspect walk happily found it.
	started := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, stmt := range fn.Body.List {
			gs, ok := stmt.(*ast.GoStmt)
			if ok && mentions(gs.Call, "runRefusalReporter") {
				started = true
			}
		}
	}
	require.True(t, started,
		"main.go has no `go runRefusalReporter(...)` statement directly in a function body: refused "+
			"connections are counted and never surfaced, so an operator sees agents fail to appear with "+
			"nothing in the log")
}
```

Add `"go/ast"`, `"go/parser"`, `"go/token"` to `grpc_config_test.go`'s imports.

Append to `cmd/relay-server/grpc_server_test.go`:

```go
// TestGRPCServer_ConnectionBeyondPerIPCapIsRefused is the end-to-end arm over
// real TCP.
//
// THE LAST TWO BLOCKS ARE THE POINT. Refusing the third connection is also what
// happens under the single most dangerous mis-implementation - returning an
// error from Accept, which grpc.Server.Serve treats as fatal
// (grpc@v1.80.0/server.go:944-951) and which kills the listener entirely. A test
// that stopped at "the third connection was refused" would pass under that
// mutation. So: Serve must not have returned, and after a slot is released a
// fresh peer must be able to open a REAL stream.
//
// RED at HEAD: with no limiter all three connections stay open and the read
// blocks until its deadline.
func TestGRPCServer_ConnectionBeyondPerIPCapIsRefused(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	lim := netlimit.Wrap(raw, netlimit.Config{MaxTotal: 100, MaxPerIP: 2})

	stub := &blockingAgentService{entered: make(chan struct{}, 8)}
	srv := grpc.NewServer(grpcServerOptions(grpcBounds{})...)
	relayv1.RegisterAgentServiceServer(srv, stub)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lim) }()
	t.Cleanup(srv.Stop)

	addr := raw.Addr().String()
	dialRaw := func() net.Conn {
		c, err := net.DialTimeout("tcp", addr, 3*time.Second)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	c1 := dialRaw()
	_ = dialRaw()
	c3 := dialRaw()

	require.NoError(t, c3.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, err = c3.Read(make([]byte, 1))
	require.Error(t, err, "the third connection from one source IP must be refused (per-IP cap is 2)")
	assert.False(t, errors.Is(err, os.ErrDeadlineExceeded),
		"the third connection stayed open: either the limiter is not wired or it admitted over the cap")

	select {
	case err := <-serveErr:
		t.Fatalf("grpc Serve RETURNED while the server should still be serving: %v. A refusal expressed "+
			"as an Accept error is fatal to Serve, so the 'cap' would be a total outage.", err)
	default:
	}

	require.NoError(t, c1.Close())
	require.Eventually(t, func() bool {
		cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return false
		}
		defer func() { _ = cc.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := relayv1.NewAgentServiceClient(cc).Connect(ctx); err != nil {
			return false
		}
		select {
		case <-stub.entered:
			return true
		case <-ctx.Done():
			return false
		}
	}, 5*time.Second, 100*time.Millisecond,
		"after releasing a slot, a fresh peer must be admitted AND able to open a real stream - which "+
			"proves the listener is still alive and the accept loop continued past the refusal")
}
```

Add `"errors"`, `"os"` and `"relay/internal/netlimit"` to `grpc_server_test.go`'s imports.

- [ ] **Step 2: Run test to verify it fails.** Run `go test ./cmd/relay-server/ -run 'TestGRPCAdmissionIsWired|TestGRPCServer_ConnectionBeyondPerIPCap' -v`. Expected: `TestGRPCAdmissionIsWiredByMain` FAILS with "the listener passed to grpcSrv.Serve(grpcLis) does not derive from netlimit.Wrap"; `TestGRPCServer_ConnectionBeyondPerIPCapIsRefused` PASSES (it builds its own limiter), which is exactly the gap R6 identified.

- [ ] **Step 3: Write minimal implementation.** Replace `cmd/relay-server/main.go:185-202` with:

```go
	// Start gRPC. Admission on this port is bounded three ways - one stream per
	// connection, a total and per-source-IP connection cap at the listener, and
	// an idle-transport reaper - because every per-connection control this
	// server ships (worker.ingestLogLimiter above all) states its budget per a
	// unit that was previously unbounded. See cmd/relay-server/grpc_config.go.
	grpcMaxConns, maxConnsMsg := parseConnLimit(
		"RELAY_GRPC_MAX_CONNS", os.Getenv("RELAY_GRPC_MAX_CONNS"), defaultGRPCMaxConns)
	if maxConnsMsg != "" {
		log.Printf("WARNING: %s", maxConnsMsg)
	}
	grpcMaxConnsPerIP, perIPMsg := parseConnLimit(
		"RELAY_GRPC_MAX_CONNS_PER_IP", os.Getenv("RELAY_GRPC_MAX_CONNS_PER_IP"), defaultGRPCMaxConnsPerIP)
	if perIPMsg != "" {
		log.Printf("WARNING: %s", perIPMsg)
	}
	grpcConnIdle, connIdleMsg := parseGRPCConnIdle(
		"RELAY_GRPC_MAX_CONN_IDLE", os.Getenv("RELAY_GRPC_MAX_CONN_IDLE"), defaultGRPCMaxConnIdle)
	if connIdleMsg != "" {
		log.Printf("WARNING: %s", connIdleMsg)
	}
	grpcBnds := grpcBounds{
		maxConns:      grpcMaxConns,
		maxConnsPerIP: grpcMaxConnsPerIP,
		maxConnIdle:   grpcConnIdle,
	}
	log.Print(grpcBoundsLine(grpcBnds))

	grpcSrv := grpc.NewServer(grpcServerOptions(grpcBnds)...)
	relayv1.RegisterAgentServiceServer(grpcSrv, agentHandler)
	grpcRawLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen gRPC: %v", err)
	}
	grpcLis := netlimit.Wrap(grpcRawLis, netlimit.Config{
		MaxTotal: grpcBnds.maxConns,
		MaxPerIP: grpcBnds.maxConnsPerIP,
	})
	go runRefusalReporter(ctx, grpcLis, grpcRefusalReportInterval)
	go func() {
		log.Printf("gRPC listening on %s", grpcAddr)
		if err := grpcSrv.Serve(grpcLis); err != nil {
			log.Printf("gRPC serve: %v", err)
		}
	}()
```

Import changes in `main.go`: **add** `"relay/internal/netlimit"`; **remove** `"google.golang.org/grpc/keepalive"` (the deleted block was its only use - confirm with `rg keepalive cmd/relay-server/main.go` before removing).

- [ ] **Step 4: Run test to verify it passes.** Run `go build ./... && go test ./cmd/relay-server/ -v`. Expected: PASS, everything.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/main.go cmd/relay-server/grpc_config_test.go cmd/relay-server/grpc_server_test.go
git commit -m "feat(server): bound gRPC connection admission at the listener"
```

---

## Task 10: `ingestLogLimiter`'s doc comment cites the bound and does the arithmetic

**Files:** Modify `internal/worker/ingest_log_limiter.go` (comment only, **zero behaviour change**). This is the item's fifth acceptance bullet, and it is the reason the total cap is load-bearing rather than decorative.

- [ ] **Step 1: Write the failing test.** None. This is a doc comment, and a test asserting on comment text would be a source-scanning guard of the exact kind this repo has already proven breakable. The verification is Task 12's `git diff --stat` showing zero non-comment lines changed in this file, plus `go test ./internal/worker/`.

- [ ] **Step 2: Confirm the current state.** Run `rg -n "ONE agent connection" internal/worker/ingest_log_limiter.go`. Expected: line 5, `// ingestLogLimiter bounds caller-driven log volume for ONE agent connection.` - an unqualified claim with nothing telling the reader what bounds connections.

- [ ] **Step 3: Write the change.** Insert this paragraph immediately after the existing line 5 opener and before `// It is two things stacked, ...`:

```go
// WHAT BOUNDS THE UNIT THIS BUDGET IS STATED PER. A bound stated per connection
// is only a bound if connections are bounded, and until 2026-08-20 they were not.
// They are now, in three places, and this budget's real ceiling is the product:
//
//   - grpcMaxConcurrentStreams = 1 (cmd/relay-server/grpc_config.go) means one
//     connection carries exactly ONE of these, not MaxUint32 of them - this type
//     is allocated per Connect call, i.e. per STREAM (handler.go:172).
//   - RELAY_GRPC_MAX_CONNS (default 1024) bounds live connections fleet-wide.
//   - RELAY_GRPC_MAX_CONNS_PER_IP (default 64) bounds them per source address.
//
// The arithmetic, out loud, because a bound nobody has multiplied is not yet a
// claim. At the defaults the fleet-wide worst case is 1024 x 16 = 16384 lines of
// burst and 1024 x 6 = 6144 lines per minute of steady state; per source address
// it is 64 x 16 = 1024 burst and 64 x 6 = 384 per minute. RAISING
// RELAY_GRPC_MAX_CONNS SCALES BOTH LINEARLY, so those two knobs are part of this
// control's threat model and not merely capacity settings. Setting either to 0
// disables that cap and removes the corresponding ceiling entirely.
//
// See docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md.
```

- [ ] **Step 4: Verify nothing else changed.** Run `go test ./internal/worker/` and `git diff -- internal/worker/ingest_log_limiter.go`. Expected: PASS, and every added line begins with `//`.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/ingest_log_limiter.go
git commit -m "docs(worker): cite the connection bound in ingestLogLimiter, with the arithmetic"
```

---

## Task 11: README - three env rows, the NAT hazard, the proxy note, the auto-enroll decision

**Files:** Modify `README.md`.

- [ ] **Step 1: Write the failing test.** None; documentation. **This task closes acceptance bullet 4 of the backlog item by written decision, so its prose is a deliverable, not a courtesy.**

- [ ] **Step 2: Add three rows to the `relay-server` env table**, immediately after the `RELAY_TASK_MAX_ASSIGNMENT` row (currently `README.md:279`), keeping the existing single-line-per-row format:

```
| `RELAY_GRPC_MAX_CONNS` | `1024` | Maximum concurrent agent gRPC connections, all sources combined. Refused connections are accepted and immediately closed at the listener, before the HTTP/2 handshake, any goroutine or any database work; the agent sees `Unavailable` and reconnects with backoff. This is what turns every per-connection bound in the server into a fleet-wide number - at the default, the worst-case caller-driven log volume is 1024 x the per-connection budget. `0` disables the cap, leaving the process file-descriptor limit as the only ceiling. Note the tradeoff a cap introduces: a full budget is a denial ceiling, which is why the per-IP cap below exists so that one source cannot fill it alone. If you front `:9090` with a proxy that already caps connections, set the caps there and `0` here. |
| `RELAY_GRPC_MAX_CONNS_PER_IP` | `64` | Maximum concurrent agent gRPC connections from any one source IP address, keyed on the TCP source address exactly as `RELAY_LOGIN_RATE_LIMIT` is - `X-Forwarded-For` is not trusted and there is no proxy assumption. **NAT hazard:** a site running more than this many agents behind one NAT gateway will see agents refused and reconnect-backoff indefinitely; the symptom is agents that never come online while the server logs a once-a-minute refusal summary, and the fix is to raise or disable this value. **Do not set it to `1`:** a reconnecting agent's new connection can arrive before the server has finished tearing the old one down. Keying is per exact address, so the cap is weaker for IPv6 hosts using privacy extensions, which can present many addresses. `0` disables. |
| `RELAY_GRPC_MAX_CONN_IDLE` | `15m` | How long an agent gRPC connection that is holding **no stream** may stay open before the server closes it. It can never terminate a connection that is doing its job - a connection holding a stream is not idle no matter how long or how quietly it lives - so a healthy agent is unaffected, and this is **not** a maximum connection age. It exists so that the connection caps above cannot be used as a parking primitive by a peer that completes the handshake and then opens nothing. A value below `1s` is kept but warned about, because a legitimate agent can be disconnected between dialing and opening its stream and will reconnect-loop. `0` disables reaping. |
```

- [ ] **Step 3: Amend the startup-sequence line** (currently `README.md:310`, "Start the gRPC server (agent connections)"):

```
3. Start the gRPC server (agent connections), with one stream per connection and the `RELAY_GRPC_MAX_CONNS` / `RELAY_GRPC_MAX_CONNS_PER_IP` admission caps applied at the listener. The effective bounds are printed unconditionally at startup.
```

- [ ] **Step 4: Add the auto-enroll trust-model paragraph.** This is the written decision that closes the item's fourth acceptance bullet, and it must use the item's own terms. Insert after the existing paragraph at `README.md:360-364` (the "Token-less auto-enrollment is the exception to that rule" one):

```
**What auto-enrollment costs, stated plainly.** Under `RELAY_ALLOW_AUTO_ENROLL=true`, any host able to
reach the gRPC port may create **one persistent `workers` row per distinct hostname it claims**, and the
hostname is caller-supplied and not validated. Those rows survive the connection that created them,
survive a server restart, and appear in every `GET /v1/workers` page and every dispatcher scan. **Nothing
bounds the total.** `RELAY_GRPC_MAX_CONNS_PER_IP` bounds how many such registrations one source address
can have *in flight at once*; it does not bound how many rows accumulate over time, because the rows
outlive their connections. This is a deliberate, recorded decision rather than an oversight: the flag is
off by default and its documented trust model is that any host able to reach gRPC is trusted. The
enrollment-token path does **not** have this property - the worker upsert and the single-use token
consume share one transaction, so one admin-issued token buys exactly one row. If you run auto-enrollment
on a network where that trust does not hold, do not; use enrollment tokens.
```

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: gRPC admission caps, the NAT hazard, and the auto-enroll row-growth decision"
```

---

## Task 12: Gates, and the mutation battery

**Files:** none. This task produces evidence.

- [ ] **Step 1: Run the full unit gate.**

```bash
go build ./... && go vet ./... && go test ./... -timeout 120s
```

Expected: PASS. Record the top-level test count and compare to `main`'s. **Any existing test whose result changes is a finding to report, not to fix** - `internal/worker`'s tests call handlers directly with no transport, and `internal/agent`'s tests build their own optionless `grpc.NewServer`, so neither should move.

- [ ] **Step 2: Run the race detector** on the two packages that gained concurrency.

```bash
CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/netlimit/ ./cmd/relay-server/ -timeout 180s
```

- [ ] **Step 3: Type-check the integration lane** (it is not run here, but a shared-signature break must not hide in it).

```bash
go vet -tags integration ./...
```

- [ ] **Step 4: Confirm the diff touched nothing forbidden.**

```bash
git diff --stat main...HEAD
```

Expected file set, and nothing else: `internal/netlimit/listener.go`, `internal/netlimit/listener_test.go`, `cmd/relay-server/grpc_config.go`, `cmd/relay-server/grpc_config_test.go`, `cmd/relay-server/grpc_server_test.go`, `cmd/relay-server/main.go`, `internal/worker/ingest_log_limiter.go`, `README.md`, and this plan's three docs. **No `.sql`, no `.sql.go`, no `models.go`, no `.proto`, no `internal/proto/**`, no migration, nothing under `web/`.**

- [ ] **Step 5: Run the mutation battery in an ISOLATED worktree.**

Never mutate the shared tree - sibling agents read it.

```bash
git worktree add --detach ../relay-mutation HEAD
cd ../relay-mutation
```

**GREEN BASELINE FIRST.** Before the first mutation, run `go test ./internal/netlimit/ ./cmd/relay-server/ -timeout 120s` and confirm PASS. Uniform results across a battery mean a broken harness, not good coverage; a compile error is not a behavioural kill and must be recorded as `NOT RUN`, never as a kill. Re-run the baseline after each revert.

The corrected matrix. Rows the spec listed that are struck are shown with why, so nobody re-adds them:

| # | Mutation | Must go RED |
|---|---|---|
| 1 | Remove `grpc.MaxConcurrentStreams` from `grpcServerOptions` | `TestGRPCServer_SecondStreamOnOneConnectionBlocks` |
| 2 | `const grpcMaxConcurrentStreams = 2` | `TestGRPCServer_SecondStreamOnOneConnectionBlocks` |
| 3 | Make `oneStreamPerConnection` return `nil` unconditionally | `TestAgentServiceHasExactlyOneStreamPerConnection` (**replaces** the spec's "add a second RPC", which needs `buf generate` and so cannot be run - R2) |
| 4 | `hostKey` returns `a.String()` (host:port) | **`TestLimitListener_PerIPCapIsKeyedOnHostNotHostPort`** |
| 5 | Delete the `MaxPerIP` branch in `admit` | `TestLimitListener_RefusesBeyondPerIPCap`, `...PerIPCapIsKeyedOnHostNotHostPort`, `TestGRPCServer_ConnectionBeyondPerIPCapIsRefused` |
| 6 | Delete the `MaxTotal` branch in `admit` | `TestLimitListener_TotalCapRefusesAcrossDistinctIPs` |
| 7 | Both `> 0` guards in `admit` become `>= 0` | `TestLimitListener_ZeroDisables` |
| 8 | `Accept` returns `fmt.Errorf("over limit")` instead of closing and looping | **`TestGRPCServer_ConnectionBeyondPerIPCapIsRefused`**, via the `serveErr` check and the fourth-connection arm. Its "third connection refused" assertion still passes under this mutation, which is exactly why those two blocks exist (R3) |
| 9 | `release` never deletes the per-IP entry at zero | **`TestLimitListener_ReleasedIPIsRemovedFromTheMap`** |
| 10 | Remove the `sync.Once` from `conn.Close` | `TestLimitListener_DoubleCloseReleasesExactlyOneSlot` |
| 11 | Comment out the body of `release` | `TestLimitListener_CloseReleasesTheSlot` |
| 12 | Delete `MaxConnectionIdle` from `grpcKeepaliveParams` | `TestGRPCServer_IdleConnectionWithNoStreamIsClosed` |
| 13 | Add `MaxConnectionAge: idle, MaxConnectionAgeGrace: 10 * time.Millisecond` to `grpcKeepaliveParams` | **`TestGRPCServer_ConnectionHoldingAStreamIsNotIdle`** (**replaces** the spec's "give MaxConnectionIdle age semantics", which is not expressible in relay code - R4) |
| 14 | Append a second `grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: b.maxConnIdle})` to the option list | `TestGRPCKeepaliveParamsKeepsTheLivenessProbe` - **only if the second option is built through `grpcKeepaliveParams`**. If it is written inline, this mutation is invisible to the test and must be recorded as **NOT KILLED**, a known limit of a constant check |
| 15 | `grpcKeepaliveMinTime = 10 * time.Second` | `TestGRPCEnforcementPolicyMatchesGRPCsOwnDefault` - a **constant** kill, not a behavioural one, and it must be reported that way (R1) |
| 16 | `log.Printf("netlimit: refused %s", key)` inside `admit` | **`TestLimitListener_RefusalWritesNothingToTheLog`** (**replaces** the spec's row 13, which pointed at the reporter test and would not have reddened - R5) |
| 17 | `refusalReporter.tick` logs unconditionally (drop the `s == r.last` guard) | `TestRefusalSummaryLogsOnlyWhenCountersMove` |
| 18 | Delete `netlimit.Wrap` from `main.go` and pass `grpcRawLis` to `Serve` | **`TestGRPCAdmissionIsWiredByMain`** (nothing else in the suite moves - R6) |
| 19 | `grpc.NewServer()` in `main.go` with no options | `TestGRPCAdmissionIsWiredByMain` |
| ~~-~~ | ~~`MinTime: 100 * time.Millisecond` must redden a behavioural pinger test~~ | **STRUCK.** No such test can exist: `grpc.WithKeepaliveParams` clamps the client ping interval to 10s and the knob is in an unimportable internal package (R1). Row 15 is the compensating constant check |

Record the observed failing test name for each row. Then:

```bash
cd ../relay
git worktree remove ../relay-mutation --force
```

- [ ] **Step 6: Commit nothing.** This task produces a report, not a diff. If a row failed to kill, that is a finding for the task report and the missing assertion is added to the owning test before the slice is verified.

---

## Backlog items to file (the conductor files these; this plan does not)

1. **`bug-2026-08-20-revoked-credential-survives-on-a-held-connection`** - `MaxConnectionAge`/`MaxConnectionAgeGrace` and the SSE equivalent, unifying or superseding `idea-2026-08-09-sse-revoked-token-keeps-streaming`. Two transports, one defect, one staleness tolerance to decide. Explicitly out of this slice: it is not admission control, it terminates connections that are working, and it costs at most one dropped log chunk per forced reconnect (`internal/agent/agent.go:159-166`) - a product decision about log fidelity, not a security one.
2. **`bug-2026-08-20-auto-enroll-worker-row-creation-is-unbounded`** - `related` to `idea-2026-06-04-cidr-allowlist-auto-enroll` and to this spec, and it must carry the finding that **the enrollment-token path is already bounded** (`internal/worker/handler.go:242-281`: the upsert and the single-use consume share one transaction, so `rows == 0` rolls the upsert back). Any fix therefore belongs on the auto-enroll path specifically, never on `UpsertWorkerByHostname`.
3. **`bug-2026-08-20-listener-wrapper-drops-tcp-user-timeout`** - NEW, found while planning (R7). Wrapping the accepted conn makes grpc-go's `SetTCPUserTimeout` type assertion fail silently, so relay loses `TCP_USER_TIMEOUT` on Linux. Low severity - the application-layer keepalive is unaffected and still reaps a dead peer at 40s - but it is a real behavioural regression introduced by this slice and it must be tracked rather than lost in a doc comment.

---

## Residual risk, consciously accepted

- **The keepalive enforcement policy ships with no behavioural test at all** (R1). A future loosening is caught only by a constant-lockstep assertion. Accepted because the value is behaviourally identical to grpc-go's own default, so the worst case of the guard failing is that relay silently keeps the behaviour it already has.
- **`TCP_USER_TIMEOUT` is lost on Linux** (R7). Bounded by the 40s application-layer keepalive; tracked by backlog item 3.
- **`RELAY_GRPC_MAX_CONNS_PER_IP = 64` is not derivable from this repo.** It is a guess about NAT topology, documented as such, generous, and reversible with `0`.
- **A total connection cap is a denial ceiling.** An attacker that fills 1024 slots locks out legitimate agents earlier than the FD limit would. Mitigated by the per-IP cap and by idle reaping, and `0`-disablable. Stated in README.
- **The transient handler-quota stall** when a client pipelines a new stream into the gap between `deleteStream` and `streamQuota.release()`. Microseconds, self-clearing, unreachable by relay's agent. Not designed around.
- **The AST wiring guard cannot see a neutered value.** `grpcBnds = grpcBounds{}` inserted above the `Wrap` call compiles and leaves everything green. Stated in the test's own comment rather than overclaimed.
- **IPv6 privacy extensions weaken the per-IP cap.** Keying is per exact address, matching `api.clientIP`. Documented, not fixed.

---

## Self-review against the spec

- **Spec section 6.1** (`MaxConcurrentStreams(1)` + guard): Tasks 4, and the guard restructured per R2.
- **6.2** (enforcement policy): Task 4, tests reduced to a constant lockstep per R1.
- **6.3** (limiting listener, host keying, refusal semantics, accounting, three knobs): Tasks 1-3, 6, 9.
- **6.4** (`MaxConnectionIdle`): Tasks 4-5, with R8's precision about what it does and does not cover.
- **6.5** (startup line + periodic refusal summary): Tasks 7-8, with R5's extra guard in `netlimit`.
- **6.6** (`ingestLogLimiter` comment): Task 10.
- **Section 5.1 / 5.2** (`MaxConnectionAge` out, auto-enroll out): honoured; README prose in Task 11; items proposed above.
- **Section 8 tests 1-13, 17-19**: all present. Tests 14-16 struck (R1). Test 3 was already "not testable, stated rather than faked" in the spec and stays that way.
- **Section 12 criteria 1-14**: all covered, with criterion 3's RED requirement amended per R1 and D3, and criterion 4's wiring claim strengthened per R6.
