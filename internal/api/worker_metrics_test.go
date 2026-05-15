package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relay/internal/metrics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildWorkerMetricsResponse_withSamples(t *testing.T) {
	store := metrics.NewStore(10)
	store.Activate("w1", time.Unix(0, 0))
	store.Append("w1", metrics.Sample{
		At: time.Unix(10, 0), CPUPercent: 42, MemUsedBytes: 100, MemTotalBytes: 200,
		HasGPU: true, GPUUtilPercent: 71, GPUMemUsed: 5, GPUMemTotal: 8,
	})

	resp := buildWorkerMetricsResponse("w1", store)
	assert.Equal(t, "w1", resp.WorkerID)
	assert.Equal(t, 10, resp.SampleIntervalSeconds)
	require.Len(t, resp.Samples, 1)
	assert.Equal(t, 42.0, resp.Samples[0].CPUPct)
	assert.True(t, resp.Samples[0].GPU)
	assert.Equal(t, uint64(5), resp.Samples[0].GPUMemUsed)
}

func TestBuildWorkerMetricsResponse_emptyWhenUntracked(t *testing.T) {
	resp := buildWorkerMetricsResponse("ghost", metrics.NewStore(10))
	assert.Equal(t, []metricSampleResponse{}, resp.Samples)
}

func TestBuildWorkerMetricsResponse_nilStore(t *testing.T) {
	resp := buildWorkerMetricsResponse("w1", nil)
	assert.Equal(t, []metricSampleResponse{}, resp.Samples)
}

func TestHandleGetWorkerMetrics_invalidID(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/v1/workers/not-a-uuid/metrics", nil)
	req.SetPathValue("id", "not-a-uuid")
	rec := httptest.NewRecorder()

	s.handleGetWorkerMetrics(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "invalid worker id", body["error"])
}
