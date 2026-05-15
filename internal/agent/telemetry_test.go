package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseGPUTelemetry_noOutput(t *testing.T) {
	g := parseGPUTelemetry([]byte(""))
	assert.False(t, g.hasGPU)
	assert.Zero(t, g.utilPct)
	assert.Zero(t, g.memTotal)
}

func TestParseGPUTelemetry_singleGPU(t *testing.T) {
	g := parseGPUTelemetry([]byte("45, 2048, 8192\n"))
	assert.True(t, g.hasGPU)
	assert.Equal(t, 45.0, g.utilPct)
	assert.Equal(t, uint64(2048)*1024*1024, g.memUsed)
	assert.Equal(t, uint64(8192)*1024*1024, g.memTotal)
}

func TestParseGPUTelemetry_multipleGPUs(t *testing.T) {
	// util averaged (20+80)/2 = 50; memory summed.
	g := parseGPUTelemetry([]byte("20, 1024, 8192\n80, 3072, 8192\n"))
	assert.True(t, g.hasGPU)
	assert.Equal(t, 50.0, g.utilPct)
	assert.Equal(t, uint64(4096)*1024*1024, g.memUsed)
	assert.Equal(t, uint64(16384)*1024*1024, g.memTotal)
}

func TestParseGPUTelemetry_malformedLineSkipped(t *testing.T) {
	g := parseGPUTelemetry([]byte("garbage\n45, 2048, 8192\n"))
	assert.True(t, g.hasGPU)
	assert.Equal(t, 45.0, g.utilPct)
}

func TestSampleTelemetry_populatesGPUFromExec(t *testing.T) {
	msg := sampleTelemetry(func(name string, args ...string) ([]byte, error) {
		return []byte("70, 4096, 8192\n"), nil
	})
	assert.True(t, msg.HasGpu)
	assert.Equal(t, 70.0, msg.GpuUtilPercent)
	assert.Equal(t, uint64(4096)*1024*1024, msg.GpuMemUsedBytes)
}

func TestSampleTelemetry_noGPUWhenExecFails(t *testing.T) {
	msg := sampleTelemetry(func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("nvidia-smi not found")
	})
	assert.False(t, msg.HasGpu)
	// Host memory total should still be populated by gopsutil on any real host.
	assert.Greater(t, msg.MemTotalBytes, uint64(0))
}
