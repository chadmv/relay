package worker

import (
	"testing"
	"time"

	"relay/internal/metrics"
	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleTelemetry_appendsSampleToStore(t *testing.T) {
	store := metrics.NewStore(10)
	store.Activate("w1", time.Now())
	h := &Handler{Metrics: store}

	h.handleTelemetry("w1", &relayv1.WorkerTelemetry{
		CpuPercent:     55.5,
		MemUsedBytes:   100,
		MemTotalBytes:  200,
		HasGpu:         true,
		GpuUtilPercent: 70.0,
	})

	snap := store.Snapshot("w1")
	require.Len(t, snap, 1)
	assert.Equal(t, 55.5, snap[0].CPUPercent)
	assert.Equal(t, uint64(100), snap[0].MemUsedBytes)
	assert.True(t, snap[0].HasGPU)
	assert.Equal(t, 70.0, snap[0].GPUUtilPercent)
}

func TestHandleTelemetry_nilMetricsIsSafe(t *testing.T) {
	h := &Handler{} // Metrics not set
	// Must not panic.
	h.handleTelemetry("w1", &relayv1.WorkerTelemetry{CpuPercent: 1})
}
