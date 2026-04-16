package discovery_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/discovery"

	"github.com/stretchr/testify/require"
)

func TestBrowse_noService(t *testing.T) {
	// Browse on a service name that nothing advertises; should time out quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := discovery.Browse(ctx)
	require.Error(t, err, "Browse should return an error when no service is found")
}
