package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"usagewidget/server"
)

func main() {
	cfg, err := server.LoadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	store, err := server.OpenStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer store.Close()

	var codexbar *server.CodexBarClient
	if cfg.CodexBarCmd != "" {
		codexbar = server.NewCodexBarCommandClient(cfg.CodexBarCmd)
	} else {
		codexbar = server.NewCodexBarClient(cfg.CodexBarURL)
	}

	notifier, err := server.NewNotifier(cfg)
	if err != nil {
		log.Printf("notifier: %v; continuing without push", err)
		notifier, _ = server.NewNotifier(server.Config{})
	}

	api := server.NewAPI(cfg, store, codexbar)
	poller := server.NewPoller(store, codexbar, notifier, api)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	api.SetPolling(true)
	go poller.Run(ctx)

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: api.Handler()}
	go func() {
		log.Printf("listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
