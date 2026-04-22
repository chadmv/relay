//go:build integration

package scheduler_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"relay/internal/scheduler"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifyListener_TriggersOnNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool := newTestPoolFromQueries(t)

	var triggered atomic.Int32
	l := scheduler.NewNotifyListener(pool, func() {
		triggered.Add(1)
	})

	go l.Run(ctx)

	// Give LISTEN time to attach.
	time.Sleep(200 * time.Millisecond)

	// Fire a NOTIFY directly.
	_, err := pool.Exec(ctx, "SELECT pg_notify('relay_task_submitted', '')")
	require.NoError(t, err)

	// Should trigger within 2s.
	require.Eventually(t, func() bool {
		return triggered.Load() >= 1
	}, 2*time.Second, 20*time.Millisecond)

	// Fire the other channel.
	_, err = pool.Exec(ctx, "SELECT pg_notify('relay_task_completed', '')")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return triggered.Load() >= 2
	}, 2*time.Second, 20*time.Millisecond)

	// Unrelated channel should be ignored.
	before := triggered.Load()
	_, err = pool.Exec(ctx, "SELECT pg_notify('some_other_channel', '')")
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, before, triggered.Load())
}
