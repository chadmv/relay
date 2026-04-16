package agent

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v4/mem"
)

// Capabilities holds auto-detected hardware information for this worker node.
type Capabilities struct {
	Hostname string
	OS       string
	CPUCores int32
	RAMGB    int32
	GPUCount int32
	GPUModel string
}

// execFn is the signature for running an external command and returning its output.
type execFn func(name string, args ...string) ([]byte, error)

// Detect auto-detects hardware capabilities using real system calls.
func Detect() Capabilities {
	return detect(func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	})
}

// detect is the testable core of Detect, accepting an injected exec function.
func detect(fn execFn) Capabilities {
	caps := Capabilities{
		OS:       runtime.GOOS,
		CPUCores: int32(runtime.NumCPU()),
	}

	if h, err := os.Hostname(); err == nil {
		caps.Hostname = h
	}

	if v, err := mem.VirtualMemory(); err == nil {
		caps.RAMGB = int32(v.Total / (1024 * 1024 * 1024))
	}

	// GPU detection: nvidia-smi returns one GPU name per line.
	// Failures are silently ignored — no NVIDIA GPU is a valid configuration.
	out, err := fn("nvidia-smi", "--query-gpu=name", "--format=csv,noheader,nounits")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		seen := make(map[string]bool)
		var unique []string
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			caps.GPUCount++
			if !seen[l] {
				seen[l] = true
				unique = append(unique, l)
			}
		}
		caps.GPUModel = strings.Join(unique, ", ")
	}

	return caps
}
