package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"relay/internal/agent"
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

	// Detect hardware capabilities.
	caps := agent.Detect()

	// Resolve coordinator address.
	addr, err := resolveCoordinator(ctx, *coordinator)
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay-agent: %v\n", err)
		os.Exit(1)
	}

	// Wire up and run.
	a := agent.NewAgent(addr, caps, workerID, func(id string) error {
		return saveWorkerID(workerIDFile, id)
	})

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
func resolveCoordinator(ctx context.Context, flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	return discovery.Browse(ctx)
}
