package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
)

func main() {
	dsn := os.Getenv("RELAY_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://relay:relay@localhost:5432/relay?sslmode=disable"
	}
	httpAddr := os.Getenv("RELAY_HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = ":8080"
	}
	grpcAddr := os.Getenv("RELAY_GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = ":9090"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run migrations (migrate DSN uses pgx5 prefix).
	migrateDSN := "pgx5://" + strings.TrimPrefix(strings.TrimPrefix(dsn, "postgresql://"), "postgres://")
	if err := store.Migrate(migrateDSN); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	dbMaxConns := 25
	if v := os.Getenv("RELAY_DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dbMaxConns = n
		}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatalf("parse dsn: %v", err)
	}
	cfg.MaxConns = int32(dbMaxConns)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)

	if bootstrapEmail := os.Getenv("RELAY_BOOTSTRAP_ADMIN"); bootstrapEmail != "" {
		bootstrapPassword := os.Getenv("RELAY_BOOTSTRAP_PASSWORD")
		if bootstrapPassword == "" {
			log.Fatalf("RELAY_BOOTSTRAP_PASSWORD must be set when RELAY_BOOTSTRAP_ADMIN is set")
		}
		if err := bootstrapAdmin(ctx, q, bootstrapEmail, bootstrapPassword); err != nil {
			log.Fatalf("bootstrap admin: %v", err)
		}
	}

	broker := events.NewBroker()
	registry := worker.NewRegistry()
	dispatcher := scheduler.NewDispatcher(q, registry, broker)
	notifyListener := scheduler.NewNotifyListener(pool, dispatcher.Trigger)
	go notifyListener.Run(ctx)

	graceWindow := 2 * time.Minute
	if v := os.Getenv("RELAY_WORKER_GRACE_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			graceWindow = d
		}
	}
	grace := worker.NewGraceRegistry(graceWindow, func(workerID string) {
		var id pgtype.UUID
		if err := id.Scan(workerID); err != nil {
			return
		}
		_ = q.RequeueWorkerTasks(context.Background(), id)
		dispatcher.Trigger()
	})
	defer grace.Stop()

	// Seed grace timers for any workers with active tasks. If agents reconnect
	// within the window they reconcile normally; if not, their tasks requeue.
	if err := seedGraceTimersFromActiveTasks(ctx, q, grace); err != nil {
		log.Printf("warn: seed grace timers: %v", err)
	}

	agentHandler := worker.NewHandlerWithGrace(q, registry, broker, dispatcher.Trigger, grace)
	httpServer := api.New(pool, q, broker, registry)

	// Start gRPC.
	grpcSrv := grpc.NewServer()
	relayv1.RegisterAgentServiceServer(grpcSrv, agentHandler)
	grpcLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("listen gRPC: %v", err)
	}
	go func() {
		log.Printf("gRPC listening on %s", grpcAddr)
		if err := grpcSrv.Serve(grpcLis); err != nil {
			log.Printf("gRPC serve: %v", err)
		}
	}()

	// Start dispatcher.
	go dispatcher.Run(ctx)

	// Start HTTP.
	srv := &http.Server{Addr: httpAddr, Handler: httpServer.Handler()}
	go func() {
		log.Printf("HTTP listening on %s", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP serve: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	// Attempt a graceful gRPC stop, but fall back to a hard stop after 5 seconds.
	// Without the timeout, GracefulStop blocks until all streaming RPCs finish —
	// which means the server hangs as long as any agent is still connected.
	grpcStopped := make(chan struct{})
	go func() {
		grpcSrv.GracefulStop()
		close(grpcStopped)
	}()
	select {
	case <-grpcStopped:
	case <-time.After(5 * time.Second):
		grpcSrv.Stop()
	}

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
	fmt.Println("relay-server stopped")
}

// seedGraceTimersFromActiveTasks enumerates workers that have non-terminal
// tasks in the DB at startup and starts a grace timer for each. Agents that
// reconnect within the window reconcile; agents that don't will have their
// tasks requeued.
func seedGraceTimersFromActiveTasks(ctx context.Context, q *store.Queries, grace *worker.GraceRegistry) error {
	workerIDs, err := q.ListWorkersWithActiveTasks(ctx)
	if err != nil {
		return err
	}
	for _, wID := range workerIDs {
		grace.Start(uuidStrMain(wID))
	}
	return nil
}

// uuidStrMain converts a pgtype.UUID to its canonical string representation.
// Named with Main suffix to avoid collision with any other helper in this file.
func uuidStrMain(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
