package perforce

import (
	"errors"
	"fmt"
	"os/exec"
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
			name:    "disk full linux enospc",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: write //s/x/big.bin: no space left on device)")),
			wantSub: "out of disk space on this agent's workspace volume",
		},
		{
			name:    "disk full windows full sentence",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: There is not enough space on the disk.)")),
			wantSub: "out of disk space on this agent's workspace volume",
		},
		{
			name:    "disk full p4d phrasing",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Disk full; cannot write to depot.)")),
			wantSub: "out of disk space on this agent's workspace volume",
		},
		{
			name:    "disk full p4 client-side check",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Insufficient disk space to complete sync.)")),
			wantSub: "out of disk space on this agent's workspace volume",
		},
		// The two negatives below are what the disk cases are for. All four
		// positives pass under a classifier that matches `insufficient` and
		// `space` as independent words, and `workspace` contains `space` - so
		// that classifier reports a permissions problem as a full disk and sends
		// an operator to free space on a machine whose disk is fine.
		{
			name:    "workspace not found is not a disk problem",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Client 'relay_h_ab12' unknown - workspace not found.)")),
			wantSub: "",
		},
		{
			name:    "insufficient permissions on workspace is not a disk problem",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: insufficient permissions on workspace //s/x)")),
			wantSub: "",
		},
		{
			name:    "an unrelated sync failure still passes through",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: no such file or directory)")),
			wantSub: "",
		},
		// EVERY CASE BELOW IS BUILT BY THE PRODUCTION CONSTRUCTOR, not by a
		// hand-written "(stderr: ...)" string. A fixture that models only the
		// stderr half cannot see the args half, which is where the caller's own
		// depot paths land: jobspec.validateSourceSpec constrains a stream to a
		// `//` prefix and nothing else, so `//depot/disk full` is a legal spec.
		{
			name: "a disk phrase planted in the ARGS is not a disk problem",
			in: newP4CommandError(
				[]string{"-c", "relay_h_ab12", "sync", "//depot/disk full/...#head"},
				errors.New("exit status 1"),
				// Deliberately a stderr that does NOT echo the path back. p4 echoes
				// the offending path in some error classes, and when it does, the
				// phrase is caller-controlled again by a route this exclusion does
				// not cover; that residual is not closed here.
				"Access for user 'relay' has not been enabled by 'p4 protect'."),
			wantSub: "",
		},
		{
			name: "a server phrase planted in the ARGS is not a connectivity problem",
			in: newP4CommandError(
				[]string{"-c", "relay_h_ab12", "sync", "//depot/connect to server failed/...#head"},
				errors.New("exit status 1"),
				"no such file or directory"),
			wantSub: "",
		},
		{
			name: "a disk phrase planted in a path the CALLER put in an outer wrapper",
			in: fmt.Errorf("resolve head for %s: %w", "//depot/disk full/...",
				newP4CommandError([]string{"changes", "-m1", "//depot/disk full/...#head"},
					errors.New("exit status 1"), "no such file or directory")),
			wantSub: "",
		},
		{
			name: "a real disk failure through the producer's own shape",
			in: newP4CommandError(
				[]string{"-c", "relay_h_ab12", "sync", "//s/x/...@12345"},
				errors.New("exit status 1"),
				"write //s/x/big.bin: no space left on device"),
			wantSub: "out of disk space on this agent's workspace volume",
		},
		{
			name: "a missing binary arrives in the UNDERLYING error, never in stderr",
			in: newP4CommandError(
				[]string{"sync", "//s/x/..."},
				fmt.Errorf(`exec: "p4": %w`, exec.ErrNotFound),
				""),
			wantSub: "p4 binary not found on PATH",
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
				// IDENTITY, not errors.Is. errors.Is unwraps, so it holds for a
				// rewrapped error too and cannot see a misclassification at all -
				// which is the only thing the negative cases exist to catch. The
				// default arm returns its argument, so the interface values
				// compare equal.
				if got != tc.in {
					t.Errorf("expected the error back unchanged; got=%v (%T) in=%v (%T)", got, got, tc.in, tc.in)
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
