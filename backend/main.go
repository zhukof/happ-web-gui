package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	dataDir := env("DATA_DIR", "/data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	manager := &Manager{
		cli:        happCLI{binary: "/usr/local/bin/happ", dataDir: dataDir},
		configPath: filepath.Join(dataDir, "control.json"),
	}
	web, err := webHandler()
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              env("UI_LISTEN", ":8080"),
		Handler:           newAPI(manager, web),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	httpListener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen for HTTP on %s: %w", httpServer.Addr, err)
	}
	defer httpListener.Close()

	socks, err := net.Listen("tcp", ":11080")
	if err != nil {
		return fmt.Errorf("listen for SOCKS forwarding: %w", err)
	}
	defer socks.Close()
	httpProxy, err := net.Listen("tcp", ":11081")
	if err != nil {
		return fmt.Errorf("listen for HTTP proxy forwarding: %w", err)
	}
	defer httpProxy.Close()

	config, err := loadConfig(manager.configPath)
	if err != nil {
		manager.appendLog("Autostart configuration: " + err.Error())
	} else if config.Server != "" && ctx.Err() == nil {
		if err := manager.Start(config.Server); err != nil {
			manager.appendLog("Autostart: " + err.Error())
		}
	}

	serveErrors := make(chan error, 3)
	go func() {
		serveErrors <- serveProxy(ctx, socks, "127.0.0.1:10808")
	}()
	go func() {
		serveErrors <- serveProxy(ctx, httpProxy, "127.0.0.1:10809")
	}()
	go func() {
		log.Printf("Web UI listening on %s", httpListener.Addr())
		serveErrors <- httpServer.Serve(httpListener)
	}()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serveErrors:
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
	}

	processDone := manager.beginShutdown()
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 7*time.Second)
	defer stop()
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = httpServer.Close()
		shutdownErr = fmt.Errorf("shut down HTTP: %w", shutdownErr)
	}
	if processDone != nil {
		select {
		case <-processDone:
		case <-shutdownCtx.Done():
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("wait for HAPP to stop: %w", shutdownCtx.Err()))
		}
	}
	return errors.Join(serveErr, shutdownErr)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
