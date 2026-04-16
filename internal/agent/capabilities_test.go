package agent

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect_basicFields(t *testing.T) {
	caps := detect(func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("nvidia-smi not found")
	})

	require.NotEmpty(t, caps.Hostname)
	assert.Equal(t, runtime.GOOS, caps.OS)
	assert.Greater(t, caps.CPUCores, int32(0))
	assert.GreaterOrEqual(t, caps.RAMGB, int32(0))
	assert.Equal(t, int32(0), caps.GPUCount)
	assert.Equal(t, "", caps.GPUModel)
}

func TestDetect_singleGPU(t *testing.T) {
	caps := detect(func(name string, args ...string) ([]byte, error) {
		return []byte("NVIDIA GeForce RTX 4090\n"), nil
	})

	assert.Equal(t, int32(1), caps.GPUCount)
	assert.Equal(t, "NVIDIA GeForce RTX 4090", caps.GPUModel)
}

func TestDetect_multipleGPUs_sameModel(t *testing.T) {
	caps := detect(func(name string, args ...string) ([]byte, error) {
		return []byte("NVIDIA RTX 3090\nNVIDIA RTX 3090\nNVIDIA RTX 3090\n"), nil
	})

	assert.Equal(t, int32(3), caps.GPUCount)
	assert.Equal(t, "NVIDIA RTX 3090", caps.GPUModel)
}

func TestDetect_multipleGPUs_differentModels(t *testing.T) {
	caps := detect(func(name string, args ...string) ([]byte, error) {
		return []byte("NVIDIA RTX 3090\nNVIDIA RTX 4090\n"), nil
	})

	assert.Equal(t, int32(2), caps.GPUCount)
	assert.True(t, strings.Contains(caps.GPUModel, "NVIDIA RTX 3090"))
	assert.True(t, strings.Contains(caps.GPUModel, "NVIDIA RTX 4090"))
}

func TestDetect_nvidiaSmiArgs(t *testing.T) {
	var gotName string
	var gotArgs []string
	detect(func(name string, args ...string) ([]byte, error) {
		gotName = name
		gotArgs = args
		return nil, errors.New("not installed")
	})

	assert.Equal(t, "nvidia-smi", gotName)
	assert.Equal(t, []string{"--query-gpu=name", "--format=csv,noheader,nounits"}, gotArgs)
}
