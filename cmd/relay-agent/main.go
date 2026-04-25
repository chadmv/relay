package main

import (
	"context"
	"flag"
	"fmt"
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
			fmt.Fprintf(os.Stderr, "relay-agent: no credentials available — set RELAY_AGENT_ENROLLMENT_TOKEN for first boot, or provision the agent token file\n")
			os.Exit(1)
		}
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
			Root:     root,
			Hostname: caps.Hostname,
		})
		provider = pp

		// Start sweeper if age or disk-pressure threshold is configured.
		maxAge := parseDurationEnv(os.Getenv("RELAY_WORKSPACE_MAX_AGE"), 0)
		minFreeGB, _ := strconv.ParseInt(os.Getenv("RELAY_WORKSPACE_MIN_FREE_GB"), 10, 64)
		sweepInterval := parseDurationEnv(os.Getenv("RELAY_WORKSPACE_SWEEP_INTERVAL"), 15*time.Minute)
		if maxAge > 0 || minFreeGB > 0 {
			sw := &perforce.Sweeper{
				Root:          root,
				MaxAge:        maxAge,
				MinFreeGB:     minFreeGB,
				SweepInterval: sweepInterval,
				Client:        pp.Client(),
				ListLocked:    pp.LockedShortIDs,
				FreeDiskGB:    freeDiskGB,
			}
			go sw.Run(ctx)
		}
	}

	a := agent.NewAgent(addr, caps, workerID, creds, func(id string) error {
		return saveWorkerID(workerIDFile, id)
	}, provider)

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

var durRe = regexp.MustCompile(`^(\d+)([smhd])$`)

// parseDurationEnv parses a duration string of the form "<N><unit>" where unit is
// s (seconds), m (minutes), h (hours), or d (days). Returns fallback on empty or invalid input.
func parseDurationEnv(v string, fallback time.Duration) time.Duration {
	if v == "" {
		return fallback
	}
	m := durRe.FindStringSubmatch(v)
	if m == nil {
		return fallback
	}
	n, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "s":
		return time.Duration(n) * time.Second
	case "m":
		return time.Duration(n) * time.Minute
	case "h":
		return time.Duration(n) * time.Hour
	case "d":
		return time.Duration(n) * 24 * time.Hour
	}
	return fallback
}
