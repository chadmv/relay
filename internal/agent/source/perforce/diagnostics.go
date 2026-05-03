package perforce

import (
	"fmt"
	"strings"
)

// classifyP4Error rewraps known-bad p4 errors with operator-facing guidance.
// Unrecognized errors are returned unchanged. The classified error preserves
// the original via %w so callers can still errors.Is / errors.Unwrap.
func classifyP4Error(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "executable file not found"):
		return fmt.Errorf("p4 binary not found on PATH (install Perforce CLI on this worker or unset RELAY_WORKSPACE_ROOT): %w", err)
	case strings.Contains(msg, "perforce password (p4passwd) invalid or unset"):
		return fmt.Errorf("p4 ticket missing or invalid on this agent — operator must run 'p4 login' on the worker host: %w", err)
	case strings.Contains(msg, "your session has expired"):
		return fmt.Errorf("p4 ticket expired on this agent — operator must run 'p4 login' on the worker host: %w", err)
	case strings.Contains(msg, "connect to server failed"),
		strings.Contains(msg, "tcp connect to") && strings.Contains(msg, "failed"):
		return fmt.Errorf("cannot reach Perforce server from this agent — check P4PORT and network connectivity: %w", err)
	default:
		return err
	}
}
