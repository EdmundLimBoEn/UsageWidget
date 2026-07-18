package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"usagewidget/server"
)

func main() {
	binary := os.Getenv("CODEXBAR_BIN")
	if binary == "" {
		binary = "codexbar"
	}
	socketPath := os.Getenv("COLLECTOR_SOCKET")
	if socketPath == "" {
		socketPath = "/run/usagewidget/codexbar.sock"
	}

	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("remove stale socket: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen %s: %v", socketPath, err)
	}
	defer listener.Close()
	if err := os.Chmod(socketPath, 0660); err != nil {
		log.Fatalf("chmod socket: %v", err)
	}

	srv := &http.Server{
		Handler:           server.NewCollector(binary).Handler(),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      95 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("collector listening on %s", socketPath)
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}
