# Agent Stale-Token Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the relay-agent's authentication-failure logs diagnosable when a stale `<state-dir>/token` file shadows a freshly set enrollment token, without changing the no-fallback security behavior.

**Architecture:** Two pure string-building helpers in a new `internal/agent/messages.go`, each unit-tested as a table-driven white-box test. Thin call sites: a startup warning in `cmd/relay-agent/main.go` and a tailored `Unauthenticated` exit message in `internal/agent/agent.go` (which also removes a house-rule-violating em dash).

**Tech Stack:** Go, standard `testing` package, `make test`.

Reference spec: `docs/superpowers/specs/2026-06-05-agent-stale-token-diagnostics-design.md`

---

### Task 1: `messages.go` helpers (TDD)

**Files:**
- Create: `internal/agent/messages.go`
- Test: `internal/agent/messages_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/messages_test.go`:

```go
package agent

import (
	"strings"
	"testing"
)

func TestEnrollmentIgnoredWarning(t *testing.T) {
	const path = "/var/lib/relay-agent/token"
	tests := []struct {
		name             string
		hasAgentToken    bool
		enrollmentSet    bool
		wantEmpty        bool
		wantContainsPath bool
	}{
		{"stored token and enrollment set", true, true, false, true},
		{"stored token, no enrollment", true, false, true, false},
		{"no stored token, enrollment set", false, true, true, false},
		{"neither", false, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnrollmentIgnoredWarning(tt.hasAgentToken, tt.enrollmentSet, path)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("want empty, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("want non-empty warning, got empty")
			}
			if tt.wantContainsPath && !strings.Contains(got, path) {
				t.Errorf("warning %q does not name token path %q", got, path)
			}
			if !strings.Contains(got, "ignored") {
				t.Errorf("warning %q should explain the token is ignored", got)
			}
			if strings.ContainsRune(got, '—') {
				t.Errorf("warning %q contains an em dash", got)
			}
		})
	}
}

func TestAuthFailureMessage(t *testing.T) {
	const path = "/var/lib/relay-agent/token"
	tests := []struct {
		name           string
		hasAgentToken  bool
		hasEnrollment  bool
		wantSubstrings []string
	}{
		{"stored token rejected", true, false, []string{path, "delete that file", "RELAY_AGENT_ENROLLMENT_TOKEN", "exiting"}},
		{"enrollment token rejected", false, true, []string{"enrollment token was rejected", "exiting"}},
		{"token-less auto-enroll rejected", false, false, []string{"auto-enroll was rejected", "RELAY_ALLOW_AUTO_ENROLL", "exiting"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := authFailureMessage(tt.hasAgentToken, path, tt.hasEnrollment)
			for _, sub := range tt.wantSubstrings {
				if !strings.Contains(got, sub) {
					t.Errorf("message %q missing substring %q", got, sub)
				}
			}
			if strings.ContainsRune(got, '—') {
				t.Errorf("message %q contains an em dash", got)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/... -run 'TestEnrollmentIgnoredWarning|TestAuthFailureMessage' -v -timeout 30s`
Expected: FAIL to compile with "undefined: EnrollmentIgnoredWarning" / "undefined: authFailureMessage".

- [ ] **Step 3: Write minimal implementation**

Create `internal/agent/messages.go`:

```go
package agent

import "fmt"

// EnrollmentIgnoredWarning returns a warning when an enrollment token is set but
// will be ignored because a stored agent token already exists. "" = no warning.
func EnrollmentIgnoredWarning(hasAgentToken, enrollmentTokenSet bool, tokenPath string) string {
	if hasAgentToken && enrollmentTokenSet {
		return fmt.Sprintf("relay-agent: RELAY_AGENT_ENROLLMENT_TOKEN is set but ignored because a stored agent token already exists at %s; delete that file to re-enroll", tokenPath)
	}
	return ""
}

// authFailureMessage returns the exit log for an Unauthenticated registration
// failure, tailored to which credential was in use.
func authFailureMessage(hasAgentToken bool, tokenPath string, hasEnrollmentToken bool) string {
	switch {
	case hasAgentToken:
		return fmt.Sprintf("agent: authentication failed - stored agent token at %s was rejected; if this agent was re-provisioned, delete that file and set RELAY_AGENT_ENROLLMENT_TOKEN to re-enroll; exiting", tokenPath)
	case hasEnrollmentToken:
		return "agent: authentication failed - enrollment token was rejected (invalid, expired, or already used); exiting"
	default:
		return "agent: authentication failed - token-less auto-enroll was rejected; the server must have RELAY_ALLOW_AUTO_ENROLL enabled; exiting"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/... -run 'TestEnrollmentIgnoredWarning|TestAuthFailureMessage' -v -timeout 30s`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/agent/messages.go internal/agent/messages_test.go
git commit -m "feat(agent): add diagnosable auth-failure message helpers"
```

---

### Task 2: Wire the startup warning into `main.go`

**Files:**
- Modify: `cmd/relay-agent/main.go` (after the credential block at lines 42-49)

No new test: `package main` wiring is covered by Task 1's unit tests plus manual verification. This task is a thin call-site edit.

- [ ] **Step 1: Add the warning call**

In `cmd/relay-agent/main.go`, the existing block is:

```go
	if !creds.HasAgentToken() {
		if t := os.Getenv("RELAY_AGENT_ENROLLMENT_TOKEN"); t != "" {
			creds.SetEnrollmentToken(t)
			os.Unsetenv("RELAY_AGENT_ENROLLMENT_TOKEN") //nolint:errcheck // best-effort; token now in memory
		} else {
			log.Printf("relay-agent: no credentials available - attempting token-less auto-enroll (requires RELAY_ALLOW_AUTO_ENROLL on the server)")
		}
	}
```

Immediately AFTER that closing brace, add:

```go
	if w := agent.EnrollmentIgnoredWarning(
		creds.HasAgentToken(),
		os.Getenv("RELAY_AGENT_ENROLLMENT_TOKEN") != "",
		creds.TokenFilePath(),
	); w != "" {
		log.Print(w)
	}
```

Note: this reads `RELAY_AGENT_ENROLLMENT_TOKEN` again. That is correct - in the
`HasAgentToken()==true` path the existing block never reads or unsets the env var,
so it is still present here. In the `HasAgentToken()==false` path the var was
unset above (or was empty), so `EnrollmentIgnoredWarning` returns `""`.

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./cmd/relay-agent/`
Expected: no output (success). `agent` and `log` are already imported in this file.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-agent/main.go
git commit -m "feat(agent): warn at startup when enrollment token is shadowed by stored token"
```

---

### Task 3: Wire the tailored exit message into `agent.go`

**Files:**
- Modify: `internal/agent/agent.go:73-77` (the `Unauthenticated` branch in `Run`)

No new test: the helper is fully tested in Task 1; this swaps the call site. The
em-dash removal happens here because the old literal string is deleted entirely.

- [ ] **Step 1: Replace the exit log line**

In `internal/agent/agent.go`, the current branch is:

```go
			if status.Code(err) == codes.Unauthenticated {
				log.Printf("agent: authentication failed — token may have been revoked; exiting")
				a.runnerWG.Wait()
				return
			}
```

Replace it with:

```go
			if status.Code(err) == codes.Unauthenticated {
				log.Print(authFailureMessage(
					a.creds.HasAgentToken(),
					a.creds.TokenFilePath(),
					a.creds.EnrollmentToken() != "",
				))
				a.runnerWG.Wait()
				return
			}
```

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./internal/agent/`
Expected: no output (success).

- [ ] **Step 3: Verify the em dash is gone**

Run: `git grep -n $'—' internal/agent/agent.go`
Expected: no output (exit code 1 = no matches). If any line prints, an em dash remains - fix it.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/agent.go
git commit -m "fix(agent): tailor Unauthenticated exit message and drop em dash"
```

---

### Task 4: Full verification and close the backlog item

**Files:**
- Move: `docs/backlog/bug-2026-06-03-agent-stale-token-misleading-error.md` -> `docs/backlog/closed/`

- [ ] **Step 1: Run the agent unit tests**

Run: `go test ./internal/agent/... -timeout 60s`
Expected: `ok  	relay/internal/agent`.

- [ ] **Step 2: Build all binaries**

Run: `make build`
Expected: builds `bin/relay-server`, `bin/relay-agent`, `bin/relay` with no errors.

- [ ] **Step 3: Confirm no em dash remains in touched files**

Run: `git grep -n $'—' -- internal/agent cmd/relay-agent`
Expected: no output (exit code 1).

- [ ] **Step 4: Close the backlog item**

```bash
git mv docs/backlog/bug-2026-06-03-agent-stale-token-misleading-error.md docs/backlog/closed/
```

Then set the frontmatter `status:` field from `open` to `closed` in the moved file.

- [ ] **Step 5: Commit**

```bash
git add docs/backlog/
git commit -m "chore(backlog): close agent stale-token misleading-error item"
```

---

## Acceptance Criteria (from spec)

- [ ] Startup with both a stored token file and `RELAY_AGENT_ENROLLMENT_TOKEN` set emits a warning naming the token file path (Task 1 test + Task 2 wiring).
- [ ] `Unauthenticated` exit message names the token file path and remedy for the stored-token case, and is accurate for enrollment / auto-enroll cases (Task 1 test + Task 3 wiring).
- [ ] No-fallback security behavior preserved - the `HasAgentToken()` gate is untouched (Tasks 2-3 add no new credential paths).
- [ ] Em dash in the agent auth-failure log line replaced with a hyphen (Task 3 + Task 4 grep).
- [ ] Backlog item moved to `docs/backlog/closed/` (Task 4).
