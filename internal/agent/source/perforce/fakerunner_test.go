package perforce

import (
	"context"
	"fmt"
	"testing"
)

// spyTHelper captures Errorf calls without failing the outer test.
type spyTHelper struct {
	errors []string
}

func (s *spyTHelper) Helper() {}
func (s *spyTHelper) Errorf(format string, args ...any) {
	s.errors = append(s.errors, fmt.Sprintf(format, args...))
}

func TestFakeRunner_RunFailsOnUnknownKey(t *testing.T) {
	spy := &spyTHelper{}
	fr := newFakeP4Fixture(spy)
	_, err := fr.Run(context.Background(), "/cwd", []string{"p4", "unregistered-cmd"}, nil)
	if err == nil {
		t.Fatal("fakeRunner.Run: expected error for unregistered key, got nil")
	}
	if len(spy.errors) == 0 {
		t.Fatal("fakeRunner.Run: expected t.Errorf to be called for unregistered key, but it wasn't")
	}
}

func TestFakeRunner_StreamFailsOnUnknownKey(t *testing.T) {
	spy := &spyTHelper{}
	fr := newFakeP4Fixture(spy)
	err := fr.Stream(context.Background(), "/cwd", []string{"p4", "unregistered-cmd"}, func(string) {})
	if err == nil {
		t.Fatal("fakeRunner.Stream: expected error for unregistered key, got nil")
	}
	if len(spy.errors) == 0 {
		t.Fatal("fakeRunner.Stream: expected t.Errorf to be called for unregistered key, but it wasn't")
	}
}
