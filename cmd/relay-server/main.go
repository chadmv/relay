package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

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
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://relay:relay@localhost:5432/relay?sslmode=disable"
	}
	httpAddr := os.Getenv("HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = ":8080"
	}
	grpcAddr := os.Getenv("GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = ":9090"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run migrations (migrate DSN uses pgx5 prefix).
	migrateDSN := "pgx5" + dsn[len("postgres"):]
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
	grpcSrv.GracefulStop()
	_ = srv.Shutdown(context.Background())
	fmt.Println("relay-server stopped")
}
