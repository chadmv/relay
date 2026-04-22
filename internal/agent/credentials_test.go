package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadCredentials_EmptyWhenNoFile(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadCredentials(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c.HasAgentToken() {
		t.Fatalf("expected no agent token")
	}
}

func TestLoadCredentials_ReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("stored-token-abc\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCredentials(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !c.HasAgentToken() {
		t.Fatalf("expected HasAgentToken true")
	}
	if c.AgentToken() != "stored-token-abc" {
		t.Fatalf("got %q", c.AgentToken())
	}
}

func TestPersist_WritesWithRestrictivePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("0600 enforcement differs on Windows")
	}
	dir := t.TempDir()
	c, _ := LoadCredentials(dir)
	if err := c.Persist("new-token-xyz"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected 0600, got %o", info.Mode().Perm())
	}
	// Reload and verify.
	c2, err := LoadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c2.AgentToken() != "new-token-xyz" {
		t.Fatalf("got %q", c2.AgentToken())
	}
}

func TestSetEnrollmentToken(t *testing.T) {
	dir := t.TempDir()
	c, _ := LoadCredentials(dir)
	c.SetEnrollmentToken("enroll-1")
	if c.EnrollmentToken() != "enroll-1" {
		t.Fatal("enrollment token not set")
	}
	// After persisting an agent token, enrollment should be cleared.
	if err := c.Persist("agent-1"); err != nil {
		t.Fatal(err)
	}
	if c.EnrollmentToken() != "" {
		t.Fatalf("enrollment should be cleared after persist, got %q", c.EnrollmentToken())
	}
}
