# Token-less Auto-Enrollment Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a worker agent on a trusted network enroll with no token at all, gated by a single server-wide flag, while preserving the existing token lifecycle and revocation semantics for everything after the join.

**Architecture:** The gRPC `RegisterRequest` already carries `oneof credential { enrollment_token, agent_token }`; an *unset* oneof is the "no credential" signal. When `RELAY_ALLOW_AUTO_ENROLL=true`, the server upserts the worker by hostname and issues a normal long-lived agent token (no enrollment record consumed), refusing to revive a `revoked` worker. The agent stops exiting on "no credentials" and instead connects token-lessly; the existing reconnect loop already exits on `Unauthenticated` rejections, so fail-loud is free.

**Tech Stack:** Go, gRPC, pgx v5, sqlc, testcontainers-go (integration tests), Postgres.

**Spec:** `docs/superpowers/specs/2026-06-04-auto-enroll-mode-design.md`

---

## File Structure

- `internal/store/query/workers.sql` — add `GetWorkerByHostnameForUpdate` (row-locking read for the revoked guard). Regenerated into `internal/store/workers.sql.go` via `make generate`.
- `internal/worker/handler.go` — add `Handler.AllowAutoEnroll` field, an `errWorkerRevoked` sentinel, a `remoteAddr` helper, the `autoEnrollAndRegister` method, and the new `default`-case dispatch in `authenticateAndRegister`.
- `internal/worker/handler_auth_test.go` — integration tests for the auto-enroll paths.
- `cmd/relay-server/main.go` — parse `RELAY_ALLOW_AUTO_ENROLL` and set `agentHandler.AllowAutoEnroll`.
- `internal/agent/agent.go` — `buildRegisterRequest` leaves `Credential` unset when no token is present instead of erroring.
- `internal/agent/lifetime_test.go` — unit test for the no-credential `buildRegisterRequest` case.
- `cmd/relay-agent/main.go` — stop calling `os.Exit(1)` when no credentials exist; log and continue.
- `README.md` — document the new flag and behavior (4 locations).

---

## Task 1: Add row-locking `GetWorkerByHostnameForUpdate` store query

**Files:**
- Modify: `internal/store/query/workers.sql` (after the existing `GetWorkerByHostname`, around line 10)
- Generated (do not hand-edit): `internal/store/workers.sql.go`

- [ ] **Step 1: Add the query**

In `internal/store/query/workers.sql`, immediately after the existing `GetWorkerByHostname` block:

```sql
-- name: GetWorkerByHostname :one
SELECT * FROM workers WHERE hostname = $1;
```

add:

```sql
-- name: GetWorkerByHostnameForUpdate :one
SELECT * FROM workers WHERE hostname = $1 FOR UPDATE;
```

- [ ] **Step 2: Regenerate the store layer**

Run: `make generate`
Expected: completes with no error; `internal/store/workers.sql.go` now contains a `GetWorkerByHostnameForUpdate` method.

- [ ] **Step 3: Verify the generated function exists and compiles**

Run: `go build ./internal/store/...`
Expected: builds cleanly. (You can also confirm `grep "func (q \*Queries) GetWorkerByHostnameForUpdate" internal/store/workers.sql.go` returns a match with signature `(ctx context.Context, hostname string) (Worker, error)`.)

- [ ] **Step 4: Commit**

```bash
git add internal/store/query/workers.sql internal/store/workers.sql.go
git commit -m "feat(store): add GetWorkerByHostnameForUpdate row-locking query"
```

---

## Task 2: Server handshake — `AllowAutoEnroll` field and token-less new-host path

This task adds the flag plumbing and the auto-enroll path for a brand-new (or existing non-revoked) host. The revoked guard is added in Task 3 (TDD: this task's test does not exercise revocation).

**Files:**
- Modify: `internal/worker/handler.go`
- Test: `internal/worker/handler_auth_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/worker/handler_auth_test.go`:

```go
func TestConnect_AutoEnrollIssuesAgentToken(t *testing.T) {
	fx := newWorkerTestFixture(t)
	fx.Handler.AllowAutoEnroll = true

	stream := newMockConnectStream(t)
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "auto-enroll-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				// No credential field — token-less.
			},
		},
	})

	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()

	msg := stream.RecvFromServer(t, 5*time.Second)
	resp := msg.GetRegisterResponse()
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.WorkerId)
	assert.NotEmpty(t, resp.AgentToken)

	stream.CloseSend()
	<-done
}

func TestConnect_AutoEnrollDisabledRejectsNoCredential(t *testing.T) {
	fx := newWorkerTestFixture(t) // AllowAutoEnroll defaults to false

	stream := newMockConnectStream(t)
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "disabled-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
			},
		},
	})

	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()

	err := <-done
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestConnect_AutoEnroll -v -timeout 120s`
Expected: FAIL — `fx.Handler.AllowAutoEnroll` is an unknown field (compile error), and the auto-enroll path does not exist yet.

- [ ] **Step 3: Add the `AllowAutoEnroll` field to `Handler`**

In `internal/worker/handler.go`, inside the `Handler` struct (after the `Metrics` field, around line 53):

```go
	// AllowAutoEnroll, when true, permits agents to register with no credential
	// (token-less auto-enrollment). Set by cmd/relay-server after construction.
	AllowAutoEnroll bool
```

- [ ] **Step 4: Add the `remoteAddr` helper and `peer` import**

In `internal/worker/handler.go`, add `"google.golang.org/grpc/peer"` to the import block, then add this helper near the top of the file (e.g. just below the `errEnrollmentNotConsumable` var, around line 39):

```go
// remoteAddr returns the gRPC peer address for logging, or "unknown".
func remoteAddr(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return "unknown"
}
```

- [ ] **Step 5: Add the `autoEnrollAndRegister` method**

In `internal/worker/handler.go`, add after `reconnectAndRegister` (around line 223). NOTE: no revoked guard yet — that comes in Task 3.

```go
// autoEnrollAndRegister handles token-less enrollment when AllowAutoEnroll is
// set. It upserts the worker by hostname and issues a fresh agent token without
// consuming any enrollment record.
func (h *Handler) autoEnrollAndRegister(ctx context.Context, stream relayv1.AgentService_ConnectServer, reg *relayv1.RegisterRequest) (string, *workerSender, error) {
	rawAgent, agentHash := agentTokenGenerator()
	if rawAgent == "" || agentHash == "" {
		return "", nil, status.Errorf(codes.Internal, "token gen failed")
	}

	var workerID pgtype.UUID
	txErr := pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := h.q.WithTx(tx)

		w, err := txq.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
			Name:     reg.Hostname,
			Hostname: reg.Hostname,
			CpuCores: reg.CpuCores,
			RamGb:    reg.RamGb,
			GpuCount: reg.GpuCount,
			GpuModel: reg.GpuModel,
			Os:       reg.Os,
		})
		if err != nil {
			return fmt.Errorf("upsert worker: %w", err)
		}
		workerID = w.ID

		if err := txq.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
			ID: w.ID, AgentTokenHash: &agentHash,
		}); err != nil {
			return fmt.Errorf("set agent token: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return "", nil, txErr
	}

	log.Printf("worker: auto-enrolled worker %s (hostname=%s) from %s", uuidStr(workerID), reg.Hostname, remoteAddr(ctx))
	return h.finishRegister(ctx, stream, reg, workerID, rawAgent)
}
```

- [ ] **Step 6: Update the `authenticateAndRegister` default case**

In `internal/worker/handler.go`, replace the `default` branch of the switch in `authenticateAndRegister` (around line 129):

```go
	default:
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
```

with:

```go
	default:
		if h.AllowAutoEnroll {
			return h.autoEnrollAndRegister(ctx, stream, reg)
		}
		return "", nil, status.Errorf(codes.Unauthenticated, "auto-enroll disabled")
	}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestConnect_AutoEnroll -v -timeout 120s`
Expected: PASS for both `TestConnect_AutoEnrollIssuesAgentToken` and `TestConnect_AutoEnrollDisabledRejectsNoCredential`.

- [ ] **Step 8: Confirm the pre-existing no-credential test still passes**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestConnect_NoCredentialRejected -v -timeout 120s`
Expected: PASS (the default case still returns `codes.Unauthenticated`; only the message changed).

- [ ] **Step 9: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_auth_test.go
git commit -m "feat(worker): token-less auto-enroll path gated by AllowAutoEnroll"
```

---

## Task 3: Server handshake — revoked guard and token rotation

**Files:**
- Modify: `internal/worker/handler.go`
- Test: `internal/worker/handler_auth_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/worker/handler_auth_test.go`:

```go
func TestConnect_AutoEnrollRefusesRevokedWorker(t *testing.T) {
	fx := newWorkerTestFixture(t)
	fx.Handler.AllowAutoEnroll = true
	ctx := context.Background()

	// First: auto-enroll a worker.
	stream1 := newMockConnectStream(t)
	stream1.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "revoked-auto-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
			},
		},
	})
	done1 := make(chan error, 1)
	go func() { done1 <- fx.Handler.Connect(stream1) }()
	resp1 := stream1.RecvFromServer(t, 5*time.Second).GetRegisterResponse()
	require.NotNil(t, resp1)
	workerIDStr := resp1.WorkerId
	stream1.CloseSend()
	<-done1

	// Revoke it.
	var wID pgtype.UUID
	require.NoError(t, wID.Scan(workerIDStr))
	_, err := fx.Q.ClearWorkerAgentToken(ctx, wID)
	require.NoError(t, err)

	// Second: token-less auto-enroll of the same hostname must be rejected.
	stream2 := newMockConnectStream(t)
	stream2.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "revoked-auto-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
			},
		},
	})
	done2 := make(chan error, 1)
	go func() { done2 <- fx.Handler.Connect(stream2) }()

	err2 := <-done2
	require.Error(t, err2)
	assert.Equal(t, codes.Unauthenticated, status.Code(err2))

	// Worker remains revoked.
	w, err := fx.Q.GetWorkerByHostname(ctx, "revoked-auto-host")
	require.NoError(t, err)
	assert.Equal(t, "revoked", w.Status)
}

func TestConnect_AutoEnrollRotatesTokenForExistingHost(t *testing.T) {
	fx := newWorkerTestFixture(t)
	fx.Handler.AllowAutoEnroll = true

	enroll := func() string {
		stream := newMockConnectStream(t)
		stream.SendToServer(&relayv1.AgentMessage{
			Payload: &relayv1.AgentMessage_Register{
				Register: &relayv1.RegisterRequest{
					Hostname: "rotate-host",
					CpuCores: 4, RamGb: 8, Os: "linux",
				},
			},
		})
		done := make(chan error, 1)
		go func() { done <- fx.Handler.Connect(stream) }()
		resp := stream.RecvFromServer(t, 5*time.Second).GetRegisterResponse()
		require.NotNil(t, resp)
		stream.CloseSend()
		<-done
		return resp.AgentToken
	}

	first := enroll()
	second := enroll()
	require.NotEmpty(t, first)
	require.NotEmpty(t, second)
	assert.NotEqual(t, first, second, "re-enrollment should rotate the agent token")

	// Reconnect with the rotated (second) token must succeed.
	stream := newMockConnectStream(t)
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "rotate-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: second},
			},
		},
	})
	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()
	require.NotNil(t, stream.RecvFromServer(t, 5*time.Second).GetRegisterResponse())
	stream.CloseSend()
	<-done
}
```

- [ ] **Step 2: Run the tests to verify the revoked one fails**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestConnect_AutoEnrollRefusesRevokedWorker -v -timeout 120s`
Expected: FAIL — without the guard, the revoked host is re-enrolled (token issued) instead of rejected, so the `require.Error` assertion fails.

(`TestConnect_AutoEnrollRotatesTokenForExistingHost` already passes with Task 2's code; the guard must not break it.)

- [ ] **Step 3: Add the `errWorkerRevoked` sentinel**

In `internal/worker/handler.go`, next to `errEnrollmentNotConsumable` (around line 39):

```go
// errWorkerRevoked is returned inside the auto-enroll transaction when the
// existing worker row for this hostname has status 'revoked'.
var errWorkerRevoked = errors.New("worker revoked")
```

- [ ] **Step 4: Add the revoked guard to `autoEnrollAndRegister`**

In `internal/worker/handler.go`, inside the `autoEnrollAndRegister` transaction, add the guard as the FIRST statement in the `BeginTxFunc` callback, before `UpsertWorkerByHostname`:

```go
		txq := h.q.WithTx(tx)

		existing, err := txq.GetWorkerByHostnameForUpdate(ctx, reg.Hostname)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lookup worker: %w", err)
		}
		if err == nil && existing.Status == "revoked" {
			return errWorkerRevoked
		}

		w, err := txq.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
```

(The existing `w, err := txq.UpsertWorkerByHostname(...)` line stays; you are inserting the guard above it. The later `err` reuse is fine since it is reassigned by `:=`/`=` in sequence — keep the existing `w, err :=` as written.)

- [ ] **Step 5: Map the sentinel to an Unauthenticated rejection**

In `internal/worker/handler.go`, replace the post-transaction error handling in `autoEnrollAndRegister`:

```go
	if txErr != nil {
		return "", nil, txErr
	}
```

with:

```go
	if txErr != nil {
		if errors.Is(txErr, errWorkerRevoked) {
			return "", nil, status.Errorf(codes.Unauthenticated, "worker revoked")
		}
		return "", nil, txErr
	}
```

- [ ] **Step 6: Run the new tests to verify they pass**

Run: `go test -tags integration -p 1 ./internal/worker/... -run "TestConnect_AutoEnrollRefusesRevokedWorker|TestConnect_AutoEnrollRotatesTokenForExistingHost" -v -timeout 120s`
Expected: PASS for both.

- [ ] **Step 7: Run the full worker auth suite to check for regressions**

Run: `go test -tags integration -p 1 ./internal/worker/... -run TestConnect -v -timeout 180s`
Expected: PASS for all `TestConnect_*` tests (enrollment, agent-token, revoked-token, single-shot, expired, no-credential, and the four new auto-enroll tests).

- [ ] **Step 8: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_auth_test.go
git commit -m "feat(worker): refuse to auto-enroll revoked workers"
```

---

## Task 4: Wire `RELAY_ALLOW_AUTO_ENROLL` into the server

`cmd/relay-server/main.go` cannot be unit-tested in isolation here; verification is a clean build. The flag follows the exact pattern already used for `RELAY_ALLOW_SELF_REGISTER` (`strconv.ParseBool` + `log.Fatalf` on parse error).

**Files:**
- Modify: `cmd/relay-server/main.go`

- [ ] **Step 1: Set the field after constructing the handler**

In `cmd/relay-server/main.go`, immediately after the existing lines (around 138-139):

```go
	agentHandler := worker.NewHandlerWithGrace(q, pool, registry, broker, dispatcher.Trigger, grace)
	agentHandler.Metrics = metricsStore
```

add:

```go
	if v := os.Getenv("RELAY_ALLOW_AUTO_ENROLL"); v != "" {
		allow, err := strconv.ParseBool(v)
		if err != nil {
			log.Fatalf("parse RELAY_ALLOW_AUTO_ENROLL: %v", err)
		}
		agentHandler.AllowAutoEnroll = allow
	}
```

(`strconv` and `os` are already imported in this file — confirm no new imports are needed.)

- [ ] **Step 2: Build the server binary**

Run: `go build ./cmd/relay-server/...`
Expected: builds cleanly with no errors.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-server/main.go
git commit -m "feat(server): wire RELAY_ALLOW_AUTO_ENROLL into the agent handler"
```

---

## Task 5: Relax the agent's two no-credential gates

**Files:**
- Modify: `internal/agent/agent.go` (`buildRegisterRequest`, around line 259-266)
- Test: `internal/agent/lifetime_test.go`
- Modify: `cmd/relay-agent/main.go` (around line 42-50)

- [ ] **Step 1: Write the failing unit test**

Append to `internal/agent/lifetime_test.go`:

```go
func TestAgent_BuildRegisterRequest_NoCredentialsLeavesCredentialUnset(t *testing.T) {
	creds, _ := LoadCredentials(t.TempDir()) // no token file, no enrollment token
	a := NewAgent("nowhere:0", Capabilities{
		Hostname: "test", CPUCores: 1, RAMGB: 1, OS: "linux",
	}, "worker-xyz", creds, func(string) error { return nil }, nil)

	req, err := a.buildRegisterRequest()
	require.NoError(t, err)
	assert.Nil(t, req.Credential, "credential must be unset for token-less auto-enroll")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/agent/... -run TestAgent_BuildRegisterRequest_NoCredentialsLeavesCredentialUnset -v -timeout 30s`
Expected: FAIL — `buildRegisterRequest` currently returns the error `no credentials: set RELAY_AGENT_ENROLLMENT_TOKEN ...`, so `require.NoError` fails.

- [ ] **Step 3: Relax `buildRegisterRequest`**

In `internal/agent/agent.go`, replace the credential switch (around line 259-266):

```go
	switch {
	case a.creds.HasAgentToken():
		req.Credential = &relayv1.RegisterRequest_AgentToken{AgentToken: a.creds.AgentToken()}
	case a.creds.EnrollmentToken() != "":
		req.Credential = &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: a.creds.EnrollmentToken()}
	default:
		return nil, fmt.Errorf("no credentials: set RELAY_AGENT_ENROLLMENT_TOKEN or provision the agent token file")
	}
```

with:

```go
	switch {
	case a.creds.HasAgentToken():
		req.Credential = &relayv1.RegisterRequest_AgentToken{AgentToken: a.creds.AgentToken()}
	case a.creds.EnrollmentToken() != "":
		req.Credential = &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: a.creds.EnrollmentToken()}
	default:
		// No credential: token-less auto-enroll. Leave Credential unset; the
		// server accepts this only when RELAY_ALLOW_AUTO_ENROLL is enabled and
		// otherwise rejects with Unauthenticated (which the reconnect loop
		// treats as terminal).
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/agent/... -run TestAgent_BuildRegisterRequest_NoCredentialsLeavesCredentialUnset -v -timeout 30s`
Expected: PASS.

- [ ] **Step 5: Check `fmt` is still used in agent.go**

Run: `go build ./internal/agent/...`
Expected: builds cleanly. (`fmt` remains used elsewhere in the file, e.g. the `os.Stderr` paths and other `fmt.Errorf` calls — no import removal needed. If the build reports `fmt` unused, remove it from the imports; this is not expected.)

- [ ] **Step 6: Relax the `cmd/relay-agent/main.go` startup gate**

In `cmd/relay-agent/main.go`, replace the credential gate (around line 42-50):

```go
	if !creds.HasAgentToken() {
		if t := os.Getenv("RELAY_AGENT_ENROLLMENT_TOKEN"); t != "" {
			creds.SetEnrollmentToken(t)
			os.Unsetenv("RELAY_AGENT_ENROLLMENT_TOKEN") //nolint:errcheck // best-effort; token now in memory
		} else {
			fmt.Fprintf(os.Stderr, "relay-agent: no credentials available — set RELAY_AGENT_ENROLLMENT_TOKEN for first boot, or provision the agent token file\n")
			os.Exit(1)
		}
	}
```

with:

```go
	if !creds.HasAgentToken() {
		if t := os.Getenv("RELAY_AGENT_ENROLLMENT_TOKEN"); t != "" {
			creds.SetEnrollmentToken(t)
			os.Unsetenv("RELAY_AGENT_ENROLLMENT_TOKEN") //nolint:errcheck // best-effort; token now in memory
		} else {
			log.Printf("relay-agent: no credentials available — attempting token-less auto-enroll (requires RELAY_ALLOW_AUTO_ENROLL on the server)")
		}
	}
```

- [ ] **Step 7: Confirm `log` is imported in `cmd/relay-agent/main.go`**

Run: `go build ./cmd/relay-agent/...`
Expected: builds cleanly. If the build reports `log` is not imported, add `"log"` to the import block of `cmd/relay-agent/main.go`.

- [ ] **Step 8: Commit**

```bash
git add internal/agent/agent.go internal/agent/lifetime_test.go cmd/relay-agent/main.go
git commit -m "feat(agent): allow token-less startup and connect for auto-enroll"
```

---

## Task 6: Update the README

**Files:**
- Modify: `README.md` (4 locations)

- [ ] **Step 1: Document the server env var**

In `README.md`, in the server configuration / environment-variable section (the list that begins around line 264, "All configuration is via environment variables"), add an entry for the server (match the surrounding table/list format). Use this content:

```
| `RELAY_ALLOW_AUTO_ENROLL` | When `true`, agents may register with no enrollment token (token-less auto-enrollment). Intended only for trusted private networks where any host able to reach gRPC is trusted. A still-issued long-lived agent token is returned on join and used for all later reconnects. Revoked workers are not revived. Default `false`. |
```

- [ ] **Step 2: Note token-less behavior in the agent env-var table**

In `README.md`, in the agent environment-variable table (around line 352-356, the `RELAY_AGENT_ENROLLMENT_TOKEN` row), add a sentence after that row (or a short note beneath the table):

```
When the server runs with `RELAY_ALLOW_AUTO_ENROLL=true`, an agent with no `token` file and no `RELAY_AGENT_ENROLLMENT_TOKEN` will attempt token-less auto-enrollment instead of exiting. If the server does not allow it, the agent exits with an authentication error.
```

- [ ] **Step 3: Add a note to the quickstart enrollment step**

In `README.md`, in "### 4 — Enroll and start one or more agents" (around line 189-204), add a short note after the enrollment-token instructions:

```
On a trusted private network you can instead run the server with `RELAY_ALLOW_AUTO_ENROLL=true` and start the agent with no token at all — skip the `relay agent enroll` step entirely. The agent receives and persists a long-lived token on its first connection, exactly as with token enrollment.
```

- [ ] **Step 4: Note revocation behavior**

In `README.md`, in the revocation discussion (around line 342-349), add:

```
Under `RELAY_ALLOW_AUTO_ENROLL`, a revoked worker is not revived by auto-enrollment; it stays revoked until an admin clears or deletes it. (Because identity is keyed by hostname, a renamed host can still rejoin as a new worker.)
```

- [ ] **Step 5: Sanity-check the edits render**

Run: `git diff README.md`
Expected: four additions in the locations above, consistent with surrounding Markdown formatting (table rows where the surrounding content is a table, prose where it is prose). Adjust formatting to match the immediate context if needed.

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "docs: document RELAY_ALLOW_AUTO_ENROLL and token-less enrollment"
```

---

## Task 7: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Unit tests + build**

Run: `make test && make build`
Expected: all unit tests pass; all three binaries build into `bin/`.

- [ ] **Step 2: Worker integration tests**

Run: `go test -tags integration -p 1 ./internal/worker/... -timeout 300s`
Expected: PASS (requires Docker Desktop running and `p4` on PATH per repo conventions; Postgres container spins up automatically).

- [ ] **Step 3: Agent unit tests**

Run: `go test ./internal/agent/... -timeout 60s`
Expected: PASS, including the new `buildRegisterRequest` no-credential test.

- [ ] **Step 4: Confirm clean tree**

Run: `git status`
Expected: working tree clean; all changes committed across Tasks 1-6.

---

## Self-Review Notes

- **Spec coverage:** config flag (Task 4) ✓; unset-oneof signal + dispatch (Task 2) ✓; `autoEnrollAndRegister` mirroring enroll-minus-consume (Task 2) ✓; row-locked revoked guard before upsert (Tasks 1 + 3) ✓; `Unauthenticated` for both disabled and revoked (Tasks 2 + 3) ✓; agent both gates relaxed (Task 5) ✓; fail-loud via existing reconnect loop (no code, verified by design) ✓; token rotation edge (Task 3 test) ✓; observability log with RemoteAddr (Task 2) ✓; README in same change set (Task 6) ✓; tests (Tasks 2, 3, 5, 7) ✓.
- **Type consistency:** `Handler.AllowAutoEnroll bool`; `GetWorkerByHostnameForUpdate(ctx, hostname string) (store.Worker, error)`; `Worker.Status string` compared to `"revoked"`; sentinels `errWorkerRevoked` / `errEnrollmentNotConsumable`; method `autoEnrollAndRegister` used consistently across tasks.
