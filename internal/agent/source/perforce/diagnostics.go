package perforce

import (
	"errors"
	"fmt"
	"strings"
)

// classifiableText returns only the parts of err that p4 and os/exec produced.
// The command args and every caller-supplied wrapper are excluded on purpose:
// they carry depot paths straight from the job spec, which is constrained to a
// `//` prefix and no character set, so a stream named `//depot/disk full` would
// otherwise be diagnosed as a full volume and sent an operator to evict a
// healthy workspace. The underlying error is included as well as stderr because
// a missing binary is reported by os/exec and never appears in p4's stderr.
// TestClassifyP4Error's planted-phrase cases pin both halves.
func classifiableText(err error) string {
	var pe *p4CommandError
	if errors.As(err, &pe) {
		return pe.err.Error() + " " + pe.stderr
	}
	return err.Error()
}

// classifyP4Error rewraps known-bad p4 errors with operator-facing guidance.
// Unrecognized errors are returned unchanged. The classified error preserves
// the original via %w so callers can still errors.Is / errors.Unwrap.
func classifyP4Error(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(classifiableText(err))
	switch {
	case strings.Contains(msg, "executable file not found"):
		return fmt.Errorf("p4 binary not found on PATH (install Perforce CLI on this worker or unset RELAY_WORKSPACE_ROOT): %w", err)
	case strings.Contains(msg, "perforce password (p4passwd) invalid or unset"):
		return fmt.Errorf("p4 ticket missing or invalid on this agent — operator must run 'p4 login' on the worker host: %w", err)
	case strings.Contains(msg, "your session has expired"):
		return fmt.Errorf("p4 ticket expired on this agent — operator must run 'p4 login' on the worker host: %w", err)
	case strings.Contains(msg, "connect to server failed"),
		// A || (B && C): comma = case-OR, && binds tighter
		strings.Contains(msg, "tcp connect to") && strings.Contains(msg, "failed"):
		return fmt.Errorf("cannot reach Perforce server from this agent — check P4PORT and network connectivity: %w", err)
	// Match the PHRASE, never the words: `workspace` contains `space`, so an
	// independent `space` substring reports a permissions problem as a full disk.
	// TestClassifyP4Error's "insufficient permissions on workspace" case is what
	// goes RED on that mistake.
	case strings.Contains(msg, "no space left on device"),
		strings.Contains(msg, "not enough space"),
		strings.Contains(msg, "disk full"),
		strings.Contains(msg, "insufficient disk space"):
		return fmt.Errorf("out of disk space on this agent's workspace volume - free space on the worker host, or reduce the sync paths: %w", err)
	default:
		return err
	}
}
