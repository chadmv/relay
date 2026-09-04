//go:build integration

package scheduler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"relay/internal/scheduler"
	"relay/internal/testsupport/pgdsn"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// Pins the one claim in the statement-timeout decision whose failure mode is
// SILENT.
//
// NotifyListener.session runs two LISTEN statements and then blocks in
// WaitForNotification indefinitely. statement_timeout bounds statement
// EXECUTION; the two LISTENs complete at once and the wait is idle time with no
// statement running. If that reasoning is wrong, the connection is killed while
// idle, the process stops receiving cross-process dispatch wakeups, and NOTHING
// ELSE IN THE SYSTEM WOULD REPORT IT - the dispatcher would simply run on its
// poll interval forever.
//
// The timeout is 200ms and the NOTIFY arrives after 1s, so the idle wait is five
// times the timeout. A shorter margin would let this pass on a slow box for the
// wrong reason.
//
// BOUNDED FAILURE, never a hang: every wait is on a deadline and a missed wakeup
// fails with an assertion. A test that hangs here is indistinguishable from
// container trouble, which is exactly what a mutation would have to be
// distinguishable from.
//
// It has no RED against HEAD, and that is honest rather than a gap: it pins a
// claim about Postgres semantics, not about code this slice writes. The control
// assertion is what stops it passing vacuously.
func TestNotifyListener_SurvivesAStatementTimeoutShorterThanItsIdleWait(t *testing.T) {
	dsn := pgdsn.NewIntegrationDSN(t)

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.Config.RuntimeParams["statement_timeout"] = "200"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	defer pool.Close()

	// CONTROL FIRST: prove the timeout is actually armed on this pool, or every
	// assertion below is vacuous - a pool with no timeout trivially survives one.
	// One column, so a column-count mismatch cannot masquerade as the timeout.
	var slept string
	err = pool.QueryRow(context.Background(), "SELECT pg_sleep(2)::text").Scan(&slept)
	require.Error(t, err, "fixture: a 2s statement must be cancelled by a 200ms statement_timeout; "+
		"if this succeeds the runtime parameter did not reach the connection and this test proves nothing")
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "fixture: want a Postgres error, got %v", err)
	require.Equal(t, "57014", pgErr.Code,
		"fixture: the cancellation must be query_canceled, not some other failure that would make "+
			"the control pass for the wrong reason. got %s: %s", pgErr.Code, pgErr.Message)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	triggered := make(chan struct{}, 8)
	lis := scheduler.NewNotifyListener(pool, func() {
		select {
		case triggered <- struct{}{}:
		default:
		}
	})
	go lis.Run(ctx)

	// session() calls trigger() once as soon as both LISTENs attach, to drain a
	// startup gap. Consume that so the assertion below is about the NOTIFY.
	select {
	case <-triggered:
	case <-time.After(20 * time.Second):
		t.Fatal("the listener never attached: no startup trigger inside 20s")
	}

	// Idle for five times the timeout, then notify.
	time.Sleep(time.Second)
	_, err = pool.Exec(context.Background(), "NOTIFY relay_task_completed")
	require.NoError(t, err)

	select {
	case <-triggered:
	case <-time.After(20 * time.Second):
		t.Fatal("the NOTIFY produced no trigger inside 20s. If statement_timeout kills an idle " +
			"LISTEN connection, this process silently stops receiving cross-process dispatch " +
			"wakeups and nothing else reports it.")
	}
}
