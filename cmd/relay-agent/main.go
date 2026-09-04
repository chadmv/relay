package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"relay/internal/agent"
	"relay/internal/agent/source"
	"relay/internal/agent/source/perforce"
	"relay/internal/discovery"
)

func main() {
	coordinator := flag.String("coordinator", "", "coordinator host:port (skips mDNS discovery if set)")
	stateDir := flag.String("state-dir", defaultStateDir(), "directory for persistent agent state")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Load persisted worker ID (ignore not-found).
	workerIDFile := filepath.Join(*stateDir, "worker-id")
	workerID := loadWorkerID(workerIDFile)

	// Load or bootstrap credentials.
	creds, err := agent.LoadCredentials(*stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay-agent: load credentials: %v\n", err)
		os.Exit(1)
	}
	if !creds.HasAgentToken() {
		if t := os.Getenv("RELAY_AGENT_ENROLLMENT_TOKEN"); t != "" {
			creds.SetEnrollmentToken(t)
			os.Unsetenv("RELAY_AGENT_ENROLLMENT_TOKEN") //nolint:errcheck // best-effort; token now in memory
		} else {
			log.Printf("relay-agent: no credentials available - attempting token-less auto-enroll (requires RELAY_ALLOW_AUTO_ENROLL on the server)")
		}
	}
	if w := agent.EnrollmentIgnoredWarning(
		creds.HasAgentToken(),
		os.Getenv("RELAY_AGENT_ENROLLMENT_TOKEN") != "",
		creds.TokenFilePath(),
	); w != "" {
		log.Print(w)
	}

	// Detect hardware capabilities.
	caps := agent.Detect()

	// Resolve coordinator address.
	addr, err := resolveCoordinator(ctx, *coordinator)
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay-agent: %v\n", err)
		os.Exit(1)
	}

	// Wire up and run.
	// Build workspace provider if RELAY_WORKSPACE_ROOT is set.
	var provider source.Provider
	if root := os.Getenv("RELAY_WORKSPACE_ROOT"); root != "" {
		pp := perforce.New(perforce.Config{
			Root:                  root,
			Hostname:              caps.Hostname,
			SyncHeartbeatInterval: resolveSyncHeartbeatInterval(os.Getenv("RELAY_SYNC_HEARTBEAT_INTERVAL")),
			// Must stay the same helper on the same root as the sweeper's own
			// free-disk check below, or the logged figure stops being comparable
			// with RELAY_WORKSPACE_MIN_FREE_GB.
			FreeDiskGB: freeDiskGB,
		})
		if err := pp.Preflight(ctx); err != nil {
			// Non-fatal: log loudly and run without the workspace provider.
			// Source-bearing tasks are rejected by the runner at run time with
			// TASK_STATUS_PREPARE_FAILED (see Runner.Run); non-source tasks still run.
			log.Printf("relay-agent: workspace provider disabled: %v", err)
		} else {
			provider = pp

			// Start sweeper if age or disk-pressure threshold is configured.
			maxAge := parseDurationEnv("RELAY_WORKSPACE_MAX_AGE", os.Getenv("RELAY_WORKSPACE_MAX_AGE"), 0)
			minFreeGB, _ := strconv.ParseInt(os.Getenv("RELAY_WORKSPACE_MIN_FREE_GB"), 10, 64)
			sweepInterval := parseDurationEnv("RELAY_WORKSPACE_SWEEP_INTERVAL", os.Getenv("RELAY_WORKSPACE_SWEEP_INTERVAL"), 15*time.Minute)
			if maxAge > 0 || minFreeGB > 0 {
				reg, err := pp.Registry()
				if err != nil {
					log.Fatalf("workspace registry: %v", err)
				}
				sw := &perforce.Sweeper{
					Root:          root,
					Reg:           reg,
					MaxAge:        maxAge,
					MinFreeGB:     minFreeGB,
					SweepInterval: sweepInterval,
					Client:        pp.Client(),
					ListLocked:    pp.LockedShortIDs,
					FreeDiskGB:    freeDiskGB,
					Claim:         pp.ReserveForEvict,
					OnEvictedCB:   pp.InvalidateWorkspace,
				}
				go sw.Run(ctx)
			}
		}
	}

	a := agent.NewAgent(addr, caps, workerID, creds, func(id string) error {
		return saveWorkerID(workerIDFile, id)
	}, provider)

	if v := os.Getenv("RELAY_TELEMETRY_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			a.TelemetryInterval = d
		}
	}
	a.Run(ctx)
}

// defaultStateDir returns the OS-appropriate default state directory.
func defaultStateDir() string {
	if runtime.GOOS == "windows" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "relay")
	}
	return "/var/lib/relay-agent"
}

// loadWorkerID reads the persisted worker ID; returns "" if the file doesn't exist.
func loadWorkerID(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// saveWorkerID writes the worker ID to the state file, creating directories as needed.
func saveWorkerID(path, id string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(id), 0644)
}

// resolveCoordinator returns the coordinator address, either from the flag or mDNS.
func resolveCoordinator(ctx context.Context, addr string) (string, error) {
	if addr != "" {
		return addr, nil
	}
	return discovery.Browse(ctx)
}

const (
	defaultSyncHeartbeat = 30 * time.Second
	syncHeartbeatFloor   = 5 * time.Second
)

// resolveSyncHeartbeatInterval reads RELAY_SYNC_HEARTBEAT_INTERVAL. "0s"
// disables the timer. A bare "0", a negative value and any other unparseable
// input take parseDurationEnv's warn-and-fall-back path, because the shared
// regex has no unit-less or signed form. A positive value below
// syncHeartbeatFloor is refused with its own warning and falls back too: the
// only cost of this knob is durable task_logs rows, which nothing caps yet
// (docs/backlog/bug-2026-08-14-task-logs-have-no-per-task-volume-cap.md).
// TestResolveSyncHeartbeatInterval.
func resolveSyncHeartbeatInterval(v string) time.Duration {
	d := parseDurationEnv("RELAY_SYNC_HEARTBEAT_INTERVAL", v, defaultSyncHeartbeat)
	if d > 0 && d < syncHeartbeatFloor {
		log.Printf("warning: RELAY_SYNC_HEARTBEAT_INTERVAL=%q is below the %v minimum; using %v",
			v, syncHeartbeatFloor, defaultSyncHeartbeat)
		return defaultSyncHeartbeat
	}
	return d
}

var durRe = regexp.MustCompile(`^(\d+)([smhd])$`)

// parseDurationEnv parses a duration string of the form "<N><unit>" where unit is
// s (seconds), m (minutes), h (hours), or d (days). Returns fallback on empty or invalid input.
// If v is non-empty but unparseable, a warning is logged naming the env var.
//
// SYNTACTICALLY VALID IS NOT REPRESENTABLE, and the range check below is the
// only thing standing between the two. durRe admits an arbitrarily long digit
// run, so the product can leave int64 nanoseconds - and the wrapped result is
// not reliably negative, so no check on the PRODUCT can see it: 1000000000000d
// wraps to a plausible-looking positive 225 years. Every caller reads a wrapped
// value as its own kind of "off" and silently turns something off - a
// non-positive RELAY_SYNC_HEARTBEAT_INTERVAL disables the heartbeat, a
// non-positive RELAY_WORKSPACE_MAX_AGE declines to build the sweeper - with no
// warning, which is the opposite of what an operator setting the knob asked for.
// TestParseDurationEnv_AnOverflowingValueIsRefusedRatherThanWrappedNegative.
func parseDurationEnv(name, v string, fallback time.Duration) time.Duration {
	if v == "" {
		return fallback
	}
	m := durRe.FindStringSubmatch(v)
	if m == nil {
		log.Printf("warning: %s=%q is not a valid duration (want e.g. 14d, 8h, 30m); using fallback %v", name, v, fallback)
		return fallback
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		log.Printf("warning: %s=%q is larger than a duration can represent; using fallback %v", name, v, fallback)
		return fallback
	}
	var unit time.Duration
	switch m[2] {
	case "s":
		unit = time.Second
	case "m":
		unit = time.Minute
	case "h":
		unit = time.Hour
	case "d":
		unit = 24 * time.Hour
	default:
		return fallback
	}
	// n is non-negative by the regex, so division against the ceiling is the
	// whole test; it is done on the OPERAND because the product is already lost.
	if int64(n) > int64(math.MaxInt64)/int64(unit) {
		log.Printf("warning: %s=%q is larger than a duration can represent; using fallback %v", name, v, fallback)
		return fallback
	}
	return time.Duration(n) * unit
}
