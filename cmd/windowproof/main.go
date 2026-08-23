// Command windowproof is the WindowProof joint-inspection service entry point.
// It opens the SQLite WAL store, runs startup recovery, seeds the fixed
// reference catalogue, and hosts the HTTP API and health endpoint.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/windowproof/fenestration/internal/api"
	"github.com/windowproof/fenestration/internal/catalog"
	"github.com/windowproof/fenestration/internal/service"
	"github.com/windowproof/fenestration/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "windowproof.db", "SQLite database path (use :memory: for ephemeral)")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := st.Recover(ctx); err != nil {
		log.Fatalf("recovery: %v", err)
	}

	svc := service.New(st, service.WallClock{}, service.StaticInstrument{})
	if err := svc.SeedCatalog(ctx, catalog.DemoRevision()); err != nil {
		log.Fatalf("seed catalog: %v", err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.NewServer(svc).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("windowproof listening on %s (db=%s)", *addr, *dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
