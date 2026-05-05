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

	// Postgres NOTIFY is dropped on the floor if no session has LISTENed yet.
	// We don't know exactly when the listener's LISTEN has attached, so we
	// retry the NOTIFY until we observe the trigger fire. Once it fires,
	// LISTEN is definitely attached and subsequent NOTIFYs are reliable.
	sendUntilConsumed := func(channel string) {
		before := triggered.Load()
		require.Eventually(t, func() bool {
			_, err := pool.Exec(ctx, "SELECT pg_notify($1, '')", channel)
			require.NoError(t, err)
			return triggered.Load() > before
		}, 5*time.Second, 20*time.Millisecond)
	}

	sendUntilConsumed("relay_task_submitted")
	sendUntilConsumed("relay_task_completed")

	// Drain any duplicate NOTIFYs queued by the retry loops before they
	// returned. Once triggered stops changing for 200 ms the pipeline is clear.
	time.Sleep(200 * time.Millisecond)

	// Unrelated channel should be ignored. Listener is verified attached by now.
	before := triggered.Load()
	_, err := pool.Exec(ctx, "SELECT pg_notify('some_other_channel', '')")
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, before, triggered.Load())
}

func TestNotifyListener_TriggersOnceAtStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool := newTestPoolFromQueries(t)

	var triggered atomic.Int32
	l := scheduler.NewNotifyListener(pool, func() {
		triggered.Add(1)
	})

	go l.Run(ctx)

	// Without any pg_notify, the listener must still fire trigger() once
	// after LISTEN succeeds. This covers both initial startup and
	// post-reconnect dispatch-gap drain.
	require.Eventually(t, func() bool {
		return triggered.Load() >= 1
	}, 2*time.Second, 20*time.Millisecond)
}
