package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"usagewidget/server"
)

func main() {
	openusageBin := strings.TrimSpace(os.Getenv("OPENUSAGE_BIN"))
	codexbarBin := strings.TrimSpace(os.Getenv("CODEXBAR_BIN"))
	sourceHint := strings.ToLower(strings.TrimSpace(os.Getenv("COLLECTOR_SOURCE")))

	binary := firstNonEmpty(openusageBin, codexbarBin, "openusage")
	if sourceHint == "" {
		switch {
		case openusageBin != "":
			sourceHint = "openusage"
		case codexbarBin != "":
			sourceHint = "codexbar"
		default:
			sourceHint = "openusage"
		}
	}

	socketPath := os.Getenv("COLLECTOR_SOCKET")
	if socketPath == "" {
		socketPath = "/run/usagewidget/collector.sock"
	}

	var args []string
	if raw := strings.TrimSpace(os.Getenv("COLLECTOR_ARGS")); raw != "" {
		args = strings.Fields(raw)
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

	collector := server.NewCollectorForSource(binary, sourceHint, args)
	srv := &http.Server{
		Handler:           collector.Handler(),
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
	log.Printf("collector listening on %s (%s %v)", socketPath, binary, collector.Args)
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
