package perforce

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestClassifyP4Error(t *testing.T) {
	cases := []struct {
		name    string
		in      error
		wantSub string // substring expected in classified message; "" => passthrough
	}{
		{
			name:    "binary missing",
			in:      fmt.Errorf("p4 sync: %w", errors.New(`exec: "p4": executable file not found in $PATH`)),
			wantSub: "p4 binary not found on PATH",
		},
		{
			name:    "password invalid",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Perforce password (P4PASSWD) invalid or unset.)")),
			wantSub: "operator must run 'p4 login'",
		},
		{
			name:    "session expired",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Your session has expired, please login again.)")),
			wantSub: "p4 ticket expired",
		},
		{
			name:    "connect failed",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Perforce client error: Connect to server failed; check $P4PORT.)")),
			wantSub: "cannot reach Perforce server",
		},
		{
			name:    "tcp connect failed",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: TCP connect to perforce.example.com:1666 failed.)")),
			wantSub: "cannot reach Perforce server",
		},
		{
			name:    "passthrough",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: File(s) not in client view.)")),
			wantSub: "",
		},
		{
			name:    "nil",
			in:      nil,
			wantSub: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyP4Error(tc.in)
			if tc.in == nil {
				if got != nil {
					t.Fatalf("nil input must yield nil, got %v", got)
				}
				return
			}
			if tc.wantSub == "" {
				// Passthrough: implementation must return err unchanged (same pointer).
				if !errors.Is(got, tc.in) {
					t.Errorf("expected passthrough (errors.Is failed); got=%v in=%v", got, tc.in)
				}
				return
			}
			if !strings.Contains(got.Error(), tc.wantSub) {
				t.Errorf("missing %q in classified message: %v", tc.wantSub, got)
			}
			if !errors.Is(got, tc.in) {
				t.Error("classified error must wrap original via %w (errors.Is failed)")
			}
		})
	}
}
