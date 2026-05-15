package agent

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// execCommand runs an external command and returns its combined stdout. It
// matches the execFn signature defined in capabilities.go.
func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// gpuTelemetry holds parsed nvidia-smi utilization output.
type gpuTelemetry struct {
	hasGPU   bool
	utilPct  float64 // averaged across GPUs
	memUsed  uint64  // bytes, summed across GPUs
	memTotal uint64  // bytes, summed across GPUs
}

// parseGPUTelemetry parses the CSV output of
// `nvidia-smi --query-gpu=utilization.gpu,memory.used,memory.total
// --format=csv,noheader,nounits`. One line per GPU: "util, memUsedMiB,
// memTotalMiB". Utilization is averaged and memory summed across GPUs; memory
// values are converted from MiB to bytes. Malformed lines are skipped.
func parseGPUTelemetry(out []byte) gpuTelemetry {
	var g gpuTelemetry
	var utilSum float64
	var count int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) != 3 {
			continue
		}
		util, err1 := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
		memU, err2 := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
		memT, err3 := strconv.ParseUint(strings.TrimSpace(fields[2]), 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		count++
		utilSum += util
		g.memUsed += memU * 1024 * 1024
		g.memTotal += memT * 1024 * 1024
	}
	if count > 0 {
		g.hasGPU = true
		g.utilPct = utilSum / float64(count)
	}
	return g
}

// sampleTelemetry takes one host-utilization reading. CPU and memory come from
// gopsutil; GPU data comes from running nvidia-smi via the injected exec fn.
// Any individual source that errors leaves its fields at zero — the sampler
// never fails.
func sampleTelemetry(execFn execFn) *relayv1.WorkerTelemetry {
	t := &relayv1.WorkerTelemetry{}

	// cpu.Percent(0, false) reports usage since the previous call (non-blocking).
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		t.CpuPercent = pcts[0]
	}
	if v, err := mem.VirtualMemory(); err == nil {
		t.MemUsedBytes = v.Used
		t.MemTotalBytes = v.Total
	}
	if out, err := execFn("nvidia-smi",
		"--query-gpu=utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits"); err == nil {
		g := parseGPUTelemetry(out)
		t.HasGpu = g.hasGPU
		t.GpuUtilPercent = g.utilPct
		t.GpuMemUsedBytes = g.memUsed
		t.GpuMemTotalBytes = g.memTotal
	}
	return t
}

// runTelemetry samples utilization every TelemetryInterval and enqueues each
// reading on sendCh. If sendCh is full it drops the sample — telemetry is
// lossy by nature and must never block the agent's single send goroutine.
func (a *Agent) runTelemetry(ctx context.Context) {
	// Prime cpu.Percent so the first ticked reading reflects a real delta.
	_, _ = cpu.Percent(0, false)

	t := time.NewTicker(a.TelemetryInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			msg := &relayv1.AgentMessage{
				Payload: &relayv1.AgentMessage_Telemetry{
					Telemetry: sampleTelemetry(execCommand),
				},
			}
			select {
			case a.sendCh <- msg:
			default: // sendCh full — drop this sample
			}
		}
	}
}
