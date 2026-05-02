//go:build integration

package perforce

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// p4dHandle holds the connection parameters for a running p4d test
// container. P4User is always "perforce" (the only account the entrypoint
// creates); P4Port is host:port reachable from the test process;
// ShelvedCL is the changelist number the entrypoint shelved during setup.
type p4dHandle struct {
	P4Port    string
	P4User    string
	ShelvedCL int64
}

// startP4dContainer builds and starts the p4d test image, waits for it to
// be ready, and returns connection parameters. The container is terminated
// via t.Cleanup.
//
// Skips (does not fail) the test when:
//   - the `p4` client binary is not on PATH (the test process needs it because
//     the agent code under test shells out to `p4` via os/exec)
//   - Docker is not reachable on the host
//
// All other errors fail the test.
func startP4dContainer(t *testing.T) p4dHandle {
	t.Helper()

	if _, err := exec.LookPath("p4"); err != nil {
		t.Skip("p4 client binary required on PATH")
	}

	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context: "testdata/p4d",
		},
		ExposedPorts: []string{"1666/tcp"},
		WaitingFor:   wait.ForLog("p4d ready").WithStartupTimeout(2 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if isDockerUnavailable(err) {
			t.Skipf("Docker required: %v", err)
		}
		t.Fatalf("p4d container start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	host, err := container.Host(ctx)
	require.NoError(t, err, "container.Host")
	port, err := container.MappedPort(ctx, "1666/tcp")
	require.NoError(t, err, "container.MappedPort")

	return p4dHandle{
		P4Port:    host + ":" + port.Port(),
		P4User:    "perforce",
		ShelvedCL: readShelvedCL(t, ctx, container),
	}
}

// readShelvedCL reads /var/p4root/shelved-cl.txt out of the container and
// parses it as an int64.
func readShelvedCL(t *testing.T, ctx context.Context, container testcontainers.Container) int64 {
	t.Helper()
	rc, err := container.CopyFileFromContainer(ctx, "/var/p4root/shelved-cl.txt")
	require.NoError(t, err, "CopyFileFromContainer shelved-cl.txt")
	defer rc.Close()
	data, err := io.ReadAll(rc)
	require.NoError(t, err, "read shelved-cl.txt")
	cl, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	require.NoError(t, err, "parse shelved CL")
	return cl
}

// expectedClientName predicts the stream-bound client name the agent will
// create in Provider.Prepare so the test can set P4CLIENT before Prepare
// runs. Calls allocateShortID directly with an empty registry so the
// helper tracks any future change to the production shortID derivation
// (including the collision-resolution loop, if it ever fires).
func expectedClientName(hostname, sourceKey string) string {
	return fmt.Sprintf("relay_%s_%s", hostname, allocateShortID(sourceKey, &Registry{}))
}

// isDockerUnavailable inspects an error from testcontainers-go to decide
// whether it indicates that Docker is unreachable on this host (legitimate
// skip) versus a hard test failure. testcontainers-go does not expose a
// typed sentinel for this, so we string-match the most common cases.
func isDockerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"cannot connect to the docker daemon",
		"docker daemon",
		"docker socket",
		"connection refused",
		"docker: not found",
		"executable file not found",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
