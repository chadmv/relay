package perforce

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestPreflight_BinaryPresent(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(name string) (string, error) {
		if name != "p4" {
			t.Fatalf("unexpected lookup: %s", name)
		}
		return "/usr/bin/p4", nil
	}
	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture()}})
	if err := p.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
}

func TestPreflight_BinaryMissing(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(name string) (string, error) {
		return "", exec.ErrNotFound
	}
	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture()}})
	err := p.Preflight(context.Background())
	if !errors.Is(err, ErrP4BinaryMissing) {
		t.Fatalf("expected errors.Is(err, ErrP4BinaryMissing) to be true, got %v", err)
	}
	if !strings.Contains(err.Error(), "RELAY_WORKSPACE_ROOT") {
		t.Errorf("error must mention RELAY_WORKSPACE_ROOT for operator guidance: %v", err)
	}
}
