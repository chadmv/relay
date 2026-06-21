# Agent Token Windows ACL Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop the long-lived agent bearer token (`%ProgramData%\relay\token`) from inheriting ProgramData's broad DACL on Windows by applying an explicit, protected (non-inherited) DACL that grants access only to the file Owner, LocalSystem, and Administrators, while leaving Unix 0600 behavior unchanged.

**Architecture:** `Credentials.Persist` currently does `os.MkdirAll(dir, 0755)` then `os.WriteFile(path, ..., 0600)`. The 0600 mode is a Unix permission bitmask that Windows ignores - the created file inherits the parent's DACL, which on `C:\ProgramData` typically grants `BUILTIN\Users` read. The fix introduces a platform-split helper, `secureTokenFile(path string) error`, in two build-tagged files mirroring the existing `proctree_windows.go` / `proctree_unix.go` pattern. On Windows the helper applies a protected DACL via `golang.org/x/sys/windows.SetNamedSecurityInfo`; on `!windows` it is a no-op (0600 from `WriteFile` already does the job). `Persist` writes the file with 0600 as today, then calls `secureTokenFile` and returns its error as a hard failure (never leave a world-readable token).

**Tech Stack:** Go 1.26.2, `os`, `path/filepath`, `golang.org/x/sys/windows` (Win32 security APIs: SDDL parsing, `SetNamedSecurityInfo`, `GetNamedSecurityInfo`, `GetAce`, `CreateWellKnownSid`). The `golang.org/x/sys` module is already a direct dependency (`go.mod` line 15, `golang.org/x/sys v0.43.0`; already imported by `internal/agent/proctree_windows.go`) - no new module is required.

---

## Slice independence

This is **backend-only**. There is no frontend code, no API, no SQL, no protobuf, no `make generate` step. It is a single sequential slice of `internal/agent` changes: the new `secureTokenFile` helper must exist in both build-tagged files and be called from `credentials.go` as one coherent set, or the package will not compile on one of the platforms. There is no Phase 3 frontend/backend parallelism to declare. The conductor should treat this as one linear backend slice.

## Invariant interactions

None of the relay Invariants apply to this change.

- **Epoch fence / single job-spec pipeline / single sender / identity-checked teardown / no interior pointers / single JSON entry point** - all are server-side or stream-lifecycle concerns. This change is client-side at-rest storage of the agent bearer token on the agent host's filesystem.
- The token whose hash the server-side `tokenhash.Hash` invariant protects is the same long-lived bearer being stored here, but that invariant governs how the *server* stores the token's hash; it says nothing about how the *agent* protects its plaintext copy on disk. This plan only narrows the on-disk ACL and does not touch any hashing path. Note this for the reviewer and move on.

## Grounding facts (confirmed by reading the repo)

- `internal/agent/credentials.go`:
  - `Credentials` struct (lines 16-20): `tokenFilePath`, `agentToken`, `enrollmentToken`.
  - `Persist(agentToken string) error` (lines 56-66): `os.MkdirAll(filepath.Dir(c.tokenFilePath), 0755)` then `os.WriteFile(c.tokenFilePath, []byte(agentToken), 0600)`, then sets `c.agentToken` and clears `c.enrollmentToken`. **This is the function to change.**
  - `LoadCredentials(stateDir string)` (lines 24-36) reads `<stateDir>/token`; unchanged by this plan.
- `cmd/relay-agent/main.go`:
  - `defaultStateDir()` (lines 122-131): on Windows returns `filepath.Join(%ProgramData%, "relay")` (default `C:\ProgramData\relay`); on Unix `/var/lib/relay-agent`.
  - `saveWorkerID(path, id)` (lines 143-148): also `MkdirAll(..., 0755)` + `os.WriteFile(path, ..., 0644)` for the worker-ID file. The worker ID is **not** a secret (it is a public identifier the server already knows), so this plan does NOT touch it. Mentioned only so the executor does not "helpfully" widen scope.
  - The state dir is created lazily by `Persist`'s `MkdirAll`; `main.go` does not pre-create it before wiring `Credentials`. So securing the dir, if done, must happen inside the agent package (in `secureTokenFile` or a sibling), not in `main.go`.
- Platform-split precedent in this same package:
  - `internal/agent/proctree_windows.go` (`//go:build windows`) imports `golang.org/x/sys/windows` and uses `windows.*` Win32 calls.
  - `internal/agent/proctree_unix.go` (`//go:build !windows`) provides the no-op / POSIX equivalent with the identical function signature.
  - Windows-only test precedent: `internal/agent/runner_cancel_windows_test.go` (`//go:build windows`, package `agent`, imports `github.com/stretchr/testify/require`).
- Existing credentials tests in `internal/agent/credentials_test.go` (no build tag, runs on all platforms):
  - `TestLoadCredentials_EmptyWhenNoFile`, `TestLoadCredentials_ReadsFile`, `TestSetEnrollmentToken` - platform-agnostic; must stay green.
  - `TestPersist_WritesWithRestrictivePerms` (lines 39-63) **skips on Windows** (`if runtime.GOOS == "windows" { t.Skip(...) }`) and asserts `info.Mode().Perm() == 0600` on Unix. This stays as the Unix-side assertion. Do not remove its skip - 0600 perm bits are meaningless on Windows and the new Windows test (Task 4) provides the Windows-side assertion.
- `golang.org/x/sys` is `v0.43.0` in this repo. The exact API surface used below was read from `C:\Users\chadv\go\pkg\mod\golang.org\x\sys@v0.43.0\windows\security_windows.go` and is confirmed present:
  - `func SecurityDescriptorFromString(sddl string) (sd *SECURITY_DESCRIPTOR, err error)` (line 1418).
  - `func (sd *SECURITY_DESCRIPTOR) DACL() (dacl *ACL, defaulted bool, err error)` (line 1209).
  - `func SetNamedSecurityInfo(objectName string, objectType SE_OBJECT_TYPE, securityInformation SECURITY_INFORMATION, owner *SID, group *SID, dacl *ACL, sacl *ACL) (ret error)` (declared line 1153, `= advapi32.SetNamedSecurityInfoW`). Signature matches the proposal exactly: pass `nil` owner/group/sacl, the parsed `dacl`, and the protected DACL flags.
  - `func GetNamedSecurityInfo(objectName string, objectType SE_OBJECT_TYPE, securityInformation SECURITY_INFORMATION) (sd *SECURITY_DESCRIPTOR, err error)` (line 1443) - used by the test to read the DACL back.
  - `func GetAce(acl *ACL, aceIndex uint32, pAce **ACCESS_ALLOWED_ACE) (err error)` (declared line 1182, `= advapi32.GetAce`) - used by the test to walk ACEs.
  - `type ACL struct { ...; AceCount uint16; ... }` (line 893; `AceCount` is the only exported field) and `type ACCESS_ALLOWED_ACE struct { Header ACE_HEADER; Mask ACCESS_MASK; SidStart uint32 }` (line 1098), with `ACE_HEADER.AceType` and `ACCESS_ALLOWED_ACE_TYPE = 0` (lines 1091, 1107) - used by the test to read each allowed-ACE's trustee SID and mask.
  - `func CreateWellKnownSid(sidType WELL_KNOWN_SID_TYPE) (*SID, error)` (line 449) and the constants `WinWorldSid = 1`, `WinAuthenticatedUserSid = 17`, `WinBuiltinUsersSid = 27` (lines 326, 342, 352) - used by the test to build the "broad principal" SIDs to assert are absent.
  - `func (sid *SID) Equals(sid2 *SID) bool` (line 283) - used to compare an ACE trustee against a well-known SID.
  - Security-information flag constants: `SE_FILE_OBJECT = 1` (line 935), `DACL_SECURITY_INFORMATION = 0x4` (line 956), `PROTECTED_DACL_SECURITY_INFORMATION = 0x80000000` (line 962).

## DACL / principal decision (final)

Apply a **protected** DACL (no inheritance from `C:\ProgramData`, so the broad `BUILTIN\Users` ACE is not picked up) granting FULL control to exactly three trustees:

- `OW` - the Object Owner (the creating principal). Granting the owner full control means the agent can always read its own token whether it runs as LocalSystem, a dedicated service account, or an ordinary interactive user - we do not need to know the runtime identity in advance.
- `SY` - LocalSystem.
- `BA` - the Administrators built-in group.

Concrete SDDL string:

```
D:PAI(A;;FA;;;OW)(A;;FA;;;SY)(A;;FA;;;BA)
```

- `D:` - DACL.
- `PAI` - `P` = SE_DACL_PROTECTED (do not inherit ACEs from the parent), `AI` = SE_DACL_AUTO_INHERITED (the descriptor was constructed honoring inheritance rules; standard companion flag). The `P` is the load-bearing part: it is what severs inheritance of ProgramData's broad ACE.
- `(A;;FA;;;<trustee>)` - Allow ACE, `FA` = FILE_ALL_ACCESS (full control), no inheritance flags (the file is a leaf, not a container).

Mechanism: build the security descriptor with `windows.SecurityDescriptorFromString(sddl)`, extract its DACL with `sd.DACL()`, then apply it with:

```go
windows.SetNamedSecurityInfo(
    path,
    windows.SE_FILE_OBJECT,
    windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
    nil, nil, dacl, nil,
)
```

This is the proposal's recommended approach verbatim, and every name above is confirmed against `x/sys@v0.43.0` (see Grounding facts). The `PROTECTED_DACL_SECURITY_INFORMATION` flag tells `SetNamedSecurityInfo` to mark the object's DACL protected (blocking inheritance) even though the DACL we hand it came from a self-relative descriptor - belt and suspenders with the SDDL `P` flag.

### Apply-after-write vs SECURITY_ATTRIBUTES-at-create (final)

Use **write with 0600 then immediately `SetNamedSecurityInfo`**, not a `SECURITY_ATTRIBUTES` passed to `CreateFile`. Rationale:

- The exposure being fixed is the *inherited* ACL. A token created via `os.WriteFile` and then re-ACLed milliseconds later is not meaningfully exploitable in that window on a single-admin agent host, and `SetNamedSecurityInfo` replaces the DACL atomically.
- The create-with-`SECURITY_ATTRIBUTES` path means abandoning `os.WriteFile` for a hand-rolled `windows.CreateFile` + write + close with an attribute struct holding a self-relative SD, which is materially more code and a second platform divergence in the write path itself. The proposal explicitly permits the write-then-tighten approach ("writing 0600 then immediately tightening the DACL is acceptable") and prefers the smaller-complexity option when the window is not the real exposure. We take that option.

### Dir vs file (final)

Secure the **token file only**. Rationale:

- The token file is the secret. Once its own DACL is protected and restricted, an attacker cannot read the token regardless of the parent dir's ACL.
- Re-ACLing `%ProgramData%\relay` is defense-in-depth but expands blast radius: that directory is shared with the worker-ID file (a non-secret) and any future state, and protecting the dir risks breaking a future component that legitimately expects to read a non-secret file there. Keep the change surgical and scoped to the secret.
- This is a deliberate, stated narrowing of the backlog item's "and ideally `%ProgramData%\relay`". See "Deviations from the proposal" at the bottom.

### Error handling (final)

On Windows, any failure in `secureTokenFile` (SDDL parse, DACL extract, or `SetNamedSecurityInfo`) is returned as a **hard error** from `Persist`. The token file has already been written 0600 at that point, so `Persist` must NOT swallow the ACL error - returning it surfaces a failed agent boot rather than silently leaving a token with an inherited, broad DACL. The caller of `Persist` (the agent register path) already treats a `Persist` error as fatal to that registration. Do not delete the partially-secured file on failure: leaving it for the operator to inspect is preferable to deleting credentials, and a retry of `Persist` re-applies the DACL (see idempotency).

### Idempotency / existing files

`Persist` can overwrite an existing token (token rotation / re-enroll). `os.WriteFile` truncates and rewrites the existing file but does **not** reset its DACL, so a file created by an older agent build would retain its stale broad ACL after a content overwrite. Because `secureTokenFile` runs on **every** `Persist` and `SetNamedSecurityInfo` *replaces* (not merges) the DACL with our protected one, every persist - first write or overwrite - ends with the correct ACL. No special "already exists" branch is needed; the unconditional re-apply is the idempotency guarantee.

---

## Task 1: Add the no-op `!windows` helper and wire `Persist` to call it

This task lands the cross-platform contract first (the no-op side) plus the `credentials.go` call, so the package keeps compiling on Unix. The Windows implementation arrives in Task 2.

**Files:**
- Create: `internal/agent/credentials_acl_unix.go` (`//go:build !windows`)
- Modify: `internal/agent/credentials.go:56-66` (`Persist`)

- [ ] **Step 1: Create the no-op helper for non-Windows**

Create `internal/agent/credentials_acl_unix.go`:

```go
//go:build !windows

package agent

// secureTokenFile tightens the OS-level access controls on the token file at
// path. On Unix the file is already created with 0600 by Persist's os.WriteFile,
// which is the correct and sufficient protection, so this is a no-op. It exists
// to match the Windows build's signature (see credentials_acl_windows.go), where
// 0600 carries no ACL meaning and an explicit DACL must be applied.
func secureTokenFile(path string) error {
	return nil
}
```

- [ ] **Step 2: Call `secureTokenFile` from `Persist`**

In `internal/agent/credentials.go`, replace the body of `Persist` (lines 56-66) with:

```go
// Persist writes the given agent token to the state file with 0600 perms,
// tightens the OS-level access controls on the file (an explicit restrictive
// DACL on Windows, where 0600 carries no ACL meaning; a no-op on Unix where
// 0600 already restricts access), and clears any in-memory enrollment token.
func (c *Credentials) Persist(agentToken string) error {
	if err := os.MkdirAll(filepath.Dir(c.tokenFilePath), 0755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	if err := os.WriteFile(c.tokenFilePath, []byte(agentToken), 0600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	if err := secureTokenFile(c.tokenFilePath); err != nil {
		return fmt.Errorf("secure token file: %w", err)
	}
	c.agentToken = agentToken
	c.enrollmentToken = ""
	return nil
}
```

The imports in `credentials.go` (`fmt`, `os`, `path/filepath`, `strings`) are unchanged and all still used.

- [ ] **Step 3: Verify the package compiles and existing tests pass on the current (non-Windows) platform**

Run (Linux/macOS agent env):
`go build ./internal/agent/... && go test ./internal/agent/... -run 'TestLoadCredentials|TestPersist|TestSetEnrollmentToken' -v -timeout 60s`

Expected: build exit 0; tests PASS. `TestPersist_WritesWithRestrictivePerms` still asserts 0600 on Unix (the no-op `secureTokenFile` does not change perms); `TestLoadCredentials_*` and `TestSetEnrollmentToken` unaffected.

- [ ] **Step 4: Confirm the Windows build is not yet broken in a way that blocks cross-compile**

Run: `GOOS=windows go build ./internal/agent/...`

Expected: **FAIL** with an undefined-`secureTokenFile` error (the Windows build has no `secureTokenFile` until Task 2). This is expected and intended at this point - Task 2 supplies it. (On PowerShell use `$env:GOOS='windows'; go build ./internal/agent/...; Remove-Item Env:\GOOS`.)

- [ ] **Step 5: (deferred) - commit happens in Task 2 after both platforms compile**

Do not commit yet: the Windows build is intentionally broken until Task 2 adds the Windows helper. Stage Task 1 and Task 2 together and commit once in Task 2 Step 4.

---

## Task 2: Implement the Windows DACL helper

**Files:**
- Create: `internal/agent/credentials_acl_windows.go` (`//go:build windows`)

- [ ] **Step 1: Create the Windows helper that applies the protected DACL**

Create `internal/agent/credentials_acl_windows.go`:

```go
//go:build windows

package agent

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// tokenFileSDDL is the security descriptor applied to the persisted agent token
// on Windows. It defines a PROTECTED DACL (the "P" flag), so the token file does
// NOT inherit C:\ProgramData's broad ACL (which typically grants BUILTIN\Users
// read access). It grants FILE_ALL_ACCESS ("FA") to:
//   - OW: the file Owner (the creating principal), so the agent can always read
//     its own token regardless of whether it runs as LocalSystem, a service
//     account, or an interactive user.
//   - SY: LocalSystem.
//   - BA: the Administrators built-in group.
const tokenFileSDDL = "D:PAI(A;;FA;;;OW)(A;;FA;;;SY)(A;;FA;;;BA)"

// secureTokenFile applies tokenFileSDDL's DACL to the token file at path,
// replacing whatever DACL it inherited at creation. On Windows the 0600 mode
// passed to os.WriteFile is meaningless, so this is the real protection. It is
// applied on every Persist (replacing, not merging, the DACL) so an overwrite of
// a token written by an older build also ends up correctly restricted. Any
// failure is returned so Persist treats it as fatal rather than leaving a
// token with a broad inherited DACL.
func secureTokenFile(path string) error {
	sd, err := windows.SecurityDescriptorFromString(tokenFileSDDL)
	if err != nil {
		return fmt.Errorf("parse token file SDDL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("extract DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, // owner unchanged
		nil, // group unchanged
		dacl,
		nil, // sacl unchanged
	); err != nil {
		return fmt.Errorf("apply DACL to %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 2: Verify the Windows build compiles (cross-compile from any platform)**

Run: `GOOS=windows go build ./internal/agent/...`
(PowerShell: `$env:GOOS='windows'; go build ./internal/agent/...; Remove-Item Env:\GOOS`)

Expected: exit 0. Both `secureTokenFile` definitions now exist (one per build tag), `Persist` resolves the call on both platforms, and all `windows.*` symbols are present in `x/sys@v0.43.0`.

- [ ] **Step 3: Verify the non-Windows build still compiles and unit tests still pass**

Run (Linux/macOS agent env):
`go build ./internal/agent/... && go test ./internal/agent/... -run 'TestLoadCredentials|TestPersist|TestSetEnrollmentToken' -v -timeout 60s`

Expected: build exit 0; tests PASS (no behavior change on Unix).

- [ ] **Step 4: Commit the coherent cross-platform set**

```bash
git add internal/agent/credentials.go internal/agent/credentials_acl_unix.go internal/agent/credentials_acl_windows.go
git commit -m "fix(agent): apply protected DACL to token file on Windows"
```

---

## Task 3: Write the Windows DACL test (it must fail before being on the real path - confirm via the helper)

This is a TDD-style step. Because the production helper already exists (Tasks 1-2 landed it as a coherent compile unit), the "failing" demonstration here is structural: the test is written to assert the end state of `Persist` on Windows, and the executor confirms it fails if `secureTokenFile` is temporarily stubbed to `return nil`, then passes with the real implementation. The test itself is the durable artifact.

**Files:**
- Create: `internal/agent/credentials_acl_windows_test.go` (`//go:build windows`)

- [ ] **Step 1: Write the Windows ACL test**

Create `internal/agent/credentials_acl_windows_test.go`:

```go
//go:build windows

package agent

import (
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// aceSID returns the trustee SID of an allowed ACE by computing the address of
// its variable-length SidStart field.
func aceSID(ace *windows.ACCESS_ALLOWED_ACE) *windows.SID {
	return (*windows.SID)(unsafe.Pointer(&ace.SidStart))
}

// readAllowedACEs reads back the DACL of the file at path and returns its
// allowed ACEs.
func readAllowedACEs(t *testing.T, path string) []*windows.ACCESS_ALLOWED_ACE {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	require.NoError(t, err, "GetNamedSecurityInfo")
	dacl, _, err := sd.DACL()
	require.NoError(t, err, "read DACL")
	require.NotNil(t, dacl, "DACL must be present")

	var aces []*windows.ACCESS_ALLOWED_ACE
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		require.NoError(t, windows.GetAce(dacl, i, &ace), "GetAce(%d)", i)
		if ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE {
			aces = append(aces, ace)
		}
	}
	return aces
}

func TestPersist_AppliesRestrictiveDACL_Windows(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadCredentials(dir)
	require.NoError(t, err)
	require.NoError(t, c.Persist("secret-agent-token"))

	path := filepath.Join(dir, "token")
	aces := readAllowedACEs(t, path)
	require.NotEmpty(t, aces, "expected at least one allowed ACE")

	// (a) No broad principal (Everyone / Authenticated Users / BUILTIN\Users)
	// may have any granted access.
	broad := []windows.WELL_KNOWN_SID_TYPE{
		windows.WinWorldSid,             // Everyone
		windows.WinAuthenticatedUserSid, // Authenticated Users
		windows.WinBuiltinUsersSid,      // BUILTIN\Users
	}
	for _, wk := range broad {
		sid, err := windows.CreateWellKnownSid(wk)
		require.NoError(t, err, "CreateWellKnownSid(%d)", wk)
		for _, ace := range aces {
			require.False(t, sid.Equals(aceSID(ace)),
				"broad principal (well-known SID type %d) must not appear in the token DACL", wk)
		}
	}

	// (b) The current process can still read the token it just wrote. This
	// exercises the owner-full-control ("OW") ACE end to end.
	c2, err := LoadCredentials(dir)
	require.NoError(t, err, "owner must be able to read the token back")
	require.Equal(t, "secret-agent-token", c2.AgentToken())
}
```

- [ ] **Step 2: Demonstrate the test fails without the real DACL (TDD red), then restore**

On the Windows host, temporarily neutralize the Windows helper to prove the test is meaningful: in `internal/agent/credentials_acl_windows.go`, comment out the `SetNamedSecurityInfo` call body and `return nil` early from `secureTokenFile` (so the file keeps its inherited ProgramData DACL).

Run (Git Bash or PowerShell on Windows):
`go test ./internal/agent/... -run TestPersist_AppliesRestrictiveDACL_Windows -v -timeout 60s`

Expected: **FAIL** on assertion (a) - `BUILTIN\Users` (or another broad principal) appears in the inherited DACL when the file lives under a ProgramData-like parent. (Note: `t.TempDir()` lives under the user profile, whose inherited DACL may already exclude broad principals, in which case (a) may pass even stubbed. If (a) does not fail from a TempDir, the executor may instead point the test's `Persist` at a subdir of `%ProgramData%\relay-test-<rand>` created for the test and `t.Cleanup`-removed, to reproduce the real inheritance. Document whichever was used. The durable assertion is the same either way.)

Then **revert the stub** - restore `secureTokenFile` to its real Task 2 body.

- [ ] **Step 3: Run the test against the real implementation (TDD green)**

Run (Windows host):
`go test ./internal/agent/... -run TestPersist -v -timeout 60s`

Expected: PASS. `TestPersist_AppliesRestrictiveDACL_Windows` passes (no broad principal in the DACL; owner can still read), and `TestPersist_WritesWithRestrictivePerms` is skipped on Windows by its existing `runtime.GOOS == "windows"` guard.

- [ ] **Step 4: Confirm the non-Windows build is unaffected by the new test file**

Run (Linux/macOS agent env): `go build ./internal/agent/... && go test ./internal/agent/... -timeout 120s`

Expected: PASS. The `//go:build windows` test file is excluded from the Unix build; all existing agent tests stay green.

- [ ] **Step 5: Commit the test**

```bash
git add internal/agent/credentials_acl_windows_test.go
git commit -m "test(agent): assert token file DACL excludes broad principals on Windows"
```

---

## Task 4: Final cross-platform verification

**Files:** none (verification only).

- [ ] **Step 1: Windows - full agent suite**

Run (Windows host): `go test ./internal/agent/... -v -timeout 120s`
Expected: PASS, including `TestPersist_AppliesRestrictiveDACL_Windows`; `TestPersist_WritesWithRestrictivePerms` shows SKIP.

- [ ] **Step 2: Linux - compile check that the Windows build still cross-compiles, plus unit tests**

Run (Linux agent env):
`GOOS=linux go build ./internal/agent/... && GOOS=windows go build ./internal/agent/... && go test ./internal/agent/... -timeout 120s`
Expected: both builds exit 0; `go test` PASS. (Confirms the `!windows` no-op path keeps 0600 behavior and the package compiles for both targets.)

- [ ] **Step 3: Repo-wide build sanity**

Run: `make build`
Expected: all three binaries build (exit 0). Confirms `cmd/relay-agent` still links against the changed `internal/agent` package.

---

## Task 5: Close the backlog item (conductor-owned, final task)

**Files:**
- Move: `docs/backlog/bug-2026-06-10-agent-token-windows-acl.md` -> `docs/backlog/closed/`

Per the relay convention, closing a backlog item is required scope, done via the `/backlog close` command (never by hand-editing `status`).

- [ ] **Step 1: Close via the backlog skill**

Preferred: run `/backlog close agent-token-windows-acl`.

The command `git mv`s the file into `docs/backlog/closed/`, stamps `status: closed`, adds `closed: 2026-06-20` and a `resolution:` line, appends a `## Resolution` section, and commits. The resolution note should record: protected DACL (`D:PAI(A;;FA;;;OW)(A;;FA;;;SY)(A;;FA;;;BA)`) applied to the token file via `SetNamedSecurityInfo` on Windows; Unix 0600 unchanged; scope limited to the token file (not the state dir); verified by `TestPersist_AppliesRestrictiveDACL_Windows`.

- [ ] **Step 2: Verify the move**

Run: `git status`
Expected: the bug file renamed into `docs/backlog/closed/`; nothing left for it in `docs/backlog/`.

- [ ] **Step 3: Commit (if the close command did not already commit)**

```bash
git add -A docs/backlog
git commit -m "backlog: close agent-token-windows-acl (fixed)"
```

---

## Self-review against the spec

- **Fix the real ACL exposure, not "document icacls":** Tasks 1-2 apply a real protected DACL via `SetNamedSecurityInfo`. No icacls fallback.
- **Build-tag split mirroring proctree_windows.go / proctree_unix.go:** `credentials_acl_windows.go` (`//go:build windows`) and `credentials_acl_unix.go` (`//go:build !windows`), each defining `secureTokenFile(path string) error`. Helper signature stated explicitly.
- **`Persist` calls one helper that does the right thing per platform; package compiles everywhere:** Task 1 Step 2 wires the call; Task 2 Step 2 and Task 4 Step 2 prove both `GOOS=windows` and `GOOS=linux` builds.
- **Unix keeps 0600:** `credentials_acl_unix.go` is a no-op; `os.WriteFile(..., 0600)` is untouched; `TestPersist_WritesWithRestrictivePerms` (Unix) still asserts 0600.
- **Exact windows API calls + SDDL, validated against installed x/sys:** DACL section gives the SDDL `D:PAI(A;;FA;;;OW)(A;;FA;;;SY)(A;;FA;;;BA)` and the `SecurityDescriptorFromString` -> `DACL()` -> `SetNamedSecurityInfo(path, SE_FILE_OBJECT, DACL_SECURITY_INFORMATION|PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)` sequence; every symbol confirmed in `x/sys@v0.43.0` (Grounding facts).
- **credentials.go change shown in full:** Task 1 Step 2.
- **Dir-vs-file decision:** stated - token file only, with rationale (deliberate narrowing of the proposal).
- **Idempotency / existing files:** stated - `secureTokenFile` runs on every Persist and `SetNamedSecurityInfo` replaces the DACL, so overwrites of an older file end correctly restricted.
- **Error handling:** stated - ACL failure is a hard error from `Persist`; never silently leave a world-readable token; do not delete the file on failure.
- **Apply-after-write vs SECURITY_ATTRIBUTES:** decided - write-then-tighten, with rationale (smallest complexity; the inherited ACL, not the sub-ms window, is the exposure).
- **Windows verification test reads the DACL back and asserts (a) no broad principal has access and (b) owner can still read; matches runner_cancel_windows_test.go style:** Task 3 (`//go:build windows`, package `agent`, `testify/require`, in-process `GetNamedSecurityInfo` + `GetAce`).
- **!windows build still compiles and existing credentials tests stay green on all platforms:** Task 1 Step 3, Task 2 Step 3, Task 3 Step 4, Task 4 Step 2.
- **Verification commands noted:** `go test ./internal/agent/... -run TestPersist -v` (Windows, Task 3/4) and `GOOS=linux go build ./internal/agent/...` (Task 4 Step 2).
- **Backend-only, no frontend:** "Slice independence" section.
- **Invariant interaction flagged (none; note tokenhash is server-side):** "Invariant interactions" section.
- **Backlog closed via git mv to closed/:** Task 5.
- **Placeholder scan:** every code step shows literal file content; no TBD/TODO/"add error handling".
- **Type/name consistency:** `secureTokenFile(path string) error` is identical across both build-tagged files and the `Persist` call site; the test helpers (`aceSID`, `readAllowedACEs`) and `tokenFileSDDL` are self-contained in their files.
