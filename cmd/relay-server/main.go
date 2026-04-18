package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/scheduler"
	"relay/internal/store"
	"relay/internal/worker"

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

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer pool.Close()

	q := store.New(pool)

	// Re-queue in-flight tasks from prior unclean shutdown.
	if err := q.RequeueAllActiveTasks(ctx); err != nil {
		log.Printf("warn: requeue active tasks: %v", err)
	}

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
	agentHandler := worker.NewHandler(q, registry, broker, dispatcher.Trigger)
	httpServer := api.New(pool, q, broker, registry, dispatcher.Trigger)

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
