package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NotifyListener subscribes to Postgres NOTIFY on the relay dispatch channels
// and invokes trigger() on each notification. It holds one dedicated pool
// connection for the duration of each listen session; on error it releases
// and reconnects with exponential backoff.
type NotifyListener struct {
	pool    *pgxpool.Pool
	trigger func()

	// sessionFn runs one listen session; nil means use the real (*NotifyListener).
	// session. Tests inject a fake to drive Run without a live Postgres.
	sessionFn func(context.Context) (listened bool, err error)
}

// NewNotifyListener constructs a listener that calls trigger() on every
// notification from any of the relay dispatch channels.
func NewNotifyListener(pool *pgxpool.Pool, trigger func()) *NotifyListener {
	return &NotifyListener{pool: pool, trigger: trigger}
}

// initialReconnectBackoff is the wait before the first reconnect attempt. It is
// a package var (not a const) only so the ordering test can seed an accumulated
// backoff; production always uses 1s.
var initialReconnectBackoff = time.Second

// reconnectSleep, when non-nil, overrides the inter-attempt wait in Run so the
// ordering test can observe the requested duration without real sleeping. It
// returns false when ctx is cancelled, telling the loop to stop. Production
// leaves it nil and Run uses a real time.After/ctx.Done select.
var reconnectSleep func(ctx context.Context, d time.Duration) bool

// nextReconnectBackoff returns the backoff before the next reconnect attempt. A
// healthy session (one where both LISTENs succeeded before the connection
// dropped) resets to 1s; an unhealthy session doubles, capped at 60s. Pure so
// the reset rule is unit-testable without a live Postgres or timing.
func nextReconnectBackoff(current time.Duration, healthy bool) time.Duration {
	if healthy {
		return time.Second
	}
	next := current * 2
	if next > 60*time.Second {
		next = 60 * time.Second
	}
	return next
}

// Run blocks until ctx is cancelled. It holds a dedicated connection from
// the pool and loops on WaitForNotification.
func (n *NotifyListener) Run(ctx context.Context) {
	session := n.session
	if n.sessionFn != nil {
		session = n.sessionFn
	}
	backoff := initialReconnectBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		listened, err := session(ctx)
		if err != nil && ctx.Err() == nil {
			// A healthy session that dropped (both LISTENs succeeded) resets the
			// wait to 1s BEFORE sleeping, so the FIRST reconnect is prompt even
			// when prior failures had ramped backoff to the cap. This
			// reset-before-sleep ordering is the fix; the post-sleep doubling
			// below preserves exponential backoff for repeated unhealthy
			// failures (and the initial 1s wait for a first unhealthy failure).
			if listened {
				backoff = time.Second
			}
			log.Printf("notify listener: %v (backoff %s)", err, backoff)
			if !reconnectWait(ctx, backoff) {
				return
			}
			backoff = nextReconnectBackoff(backoff, false)
			continue
		}
		backoff = nextReconnectBackoff(backoff, listened)
	}
}

// reconnectWait sleeps for d before the next reconnect attempt, returning false
// if ctx is cancelled during the wait (the caller should then return). The
// reconnectSleep package var lets tests observe d without real sleeping.
func reconnectWait(ctx context.Context, d time.Duration) bool {
	if reconnectSleep != nil {
		return reconnectSleep(ctx, d)
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// session acquires a connection, LISTENs, and loops on WaitForNotification
// until an error occurs or ctx is cancelled. The returned listened bool is true
// once both LISTEN statements succeeded, signalling a healthy session whose
// later drop should reset the reconnect backoff.
func (n *NotifyListener) session(ctx context.Context) (listened bool, err error) {
	conn, err := n.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()

	raw := conn.Conn()
	if _, err := raw.Exec(ctx, "LISTEN relay_task_submitted"); err != nil {
		return false, err
	}
	if _, err := raw.Exec(ctx, "LISTEN relay_task_completed"); err != nil {
		return false, err
	}

	// Both LISTENs are attached: the subscription is live. Any error after this
	// point is a drop of a healthy session.
	listened = true

	// Drain anything submitted during a startup or reconnect gap. The
	// dispatcher's Trigger is idempotent.
	n.trigger()

	for {
		if _, err := raw.WaitForNotification(ctx); err != nil {
			return listened, err
		}
		n.trigger()
	}
}
