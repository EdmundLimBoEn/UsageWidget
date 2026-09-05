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
	binary := firstNonEmpty(strings.TrimSpace(os.Getenv("CROSSUSAGE_BIN")), "crossusage-cli")

	httpAddr := strings.TrimSpace(os.Getenv("COLLECTOR_HTTP_ADDR"))
	socketPath := os.Getenv("COLLECTOR_SOCKET")
	if httpAddr == "" && socketPath == "" {
		socketPath = "/run/usagewidget/collector.sock"
	}

	var args []string
	if raw := strings.TrimSpace(os.Getenv("COLLECTOR_ARGS")); raw != "" {
		args = strings.Fields(raw)
	}

	collector := server.NewCollectorWithArgs(binary, args)
	handler := collector.Handler()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	servers := make([]*http.Server, 0, 2)
	errCh := make(chan error, 2)

	if socketPath != "" {
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
		srv := newCollectorHTTPServer(handler)
		servers = append(servers, srv)
		go func() {
			log.Printf("collector listening on %s (%s %v)", socketPath, binary, collector.Args)
			errCh <- srv.Serve(listener)
		}()
	}
	if httpAddr != "" {
		srv := newCollectorHTTPServer(handler)
		srv.Addr = httpAddr
		servers = append(servers, srv)
		go func() {
			log.Printf("collector listening on http://%s (%s %v)", httpAddr, binary, collector.Args)
			errCh <- srv.ListenAndServe()
		}()
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, srv := range servers {
			_ = srv.Shutdown(shutdownCtx)
		}
	}()

	err := <-errCh
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func newCollectorHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      250 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
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
