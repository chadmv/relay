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
}

// NewNotifyListener constructs a listener that calls trigger() on every
// notification from any of the relay dispatch channels.
func NewNotifyListener(pool *pgxpool.Pool, trigger func()) *NotifyListener {
	return &NotifyListener{pool: pool, trigger: trigger}
}

// Run blocks until ctx is cancelled. It holds a dedicated connection from
// the pool and loops on WaitForNotification.
func (n *NotifyListener) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := n.session(ctx); err != nil && ctx.Err() == nil {
			log.Printf("notify listener: %v (backoff %s)", err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			continue
		}
		backoff = time.Second
	}
}

// session acquires a connection, LISTENs, and loops on WaitForNotification
// until an error occurs or ctx is cancelled.
func (n *NotifyListener) session(ctx context.Context) error {
	conn, err := n.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	raw := conn.Conn()
	if _, err := raw.Exec(ctx, "LISTEN relay_task_submitted"); err != nil {
		return err
	}
	if _, err := raw.Exec(ctx, "LISTEN relay_task_completed"); err != nil {
		return err
	}

	// Drain anything submitted during a startup or reconnect gap. The
	// dispatcher's Trigger is idempotent.
	n.trigger()

	for {
		_, err := raw.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		n.trigger()
	}
}
