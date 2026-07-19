package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
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
	} else if cfg.CodexBarURL != "" {
		codexbar = server.NewCodexBarClient(cfg.CodexBarURL)
	} else {
		codexbar = server.NewCodexBarUnixClient(cfg.CollectorSocket)
	}

	notifier, err := server.NewNotifier(cfg)
	if err != nil {
		log.Printf("notifier: %v; continuing without push", err)
		notifier, _ = server.NewNotifier(server.Config{})
	}

	api := server.NewAPI(cfg, store, codexbar)
	poller := server.NewPoller(store, codexbar, notifier, api)
	api.SetPoller(poller)
	api.SetNotifier(notifier)

	mainServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      95 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	mainListener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Fatalf("listen %s: %v", cfg.ListenAddr, err)
	}

	servers := []*http.Server{mainServer}
	listeners := []net.Listener{mainListener}
	if cfg.DemoEnabled {
		demoAPI := server.NewDemoAPI(store, poller, cfg, notifier)
		demoServer := &http.Server{
			Addr:              cfg.DemoListenAddr,
			Handler:           demoAPI.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      95 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    16 << 10,
		}
		demoListener, err := net.Listen("tcp", cfg.DemoListenAddr)
		if err != nil {
			mainListener.Close()
			log.Fatalf("listen %s: %v", cfg.DemoListenAddr, err)
		}
		servers = append(servers, demoServer)
		listeners = append(listeners, demoListener)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	api.SetPolling(true)
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		poller.Run(ctx)
	}()

	errCh := make(chan error, len(servers))
	var serveWG sync.WaitGroup
	for i := range servers {
		srv, listener := servers[i], listeners[i]
		serveWG.Add(1)
		go func() {
			defer serveWG.Done()
			log.Printf("listening on %s", srv.Addr)
			if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		log.Printf("http server: %v", err)
		stop()
	}
	log.Printf("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var shutdownWG sync.WaitGroup
	for _, srv := range servers {
		shutdownWG.Add(1)
		go func() {
			defer shutdownWG.Done()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				log.Printf("shutdown %s: %v", srv.Addr, err)
			}
		}()
	}
	shutdownWG.Wait()
	serveWG.Wait()
	<-pollDone
}
