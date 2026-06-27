package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg, err := LoadConfig()
	if err != nil {
		slog.Error("gateway: config error", "error", err)
		os.Exit(1)
	}

	slog.Info("gateway: starting",
		"listen", cfg.ListenAddr,
		"upstream", cfg.UpstreamURL,
		"max_concurrency", cfg.MaxConcurrency,
		"max_queue_depth", cfg.MaxQueueDepth,
		"request_timeout", cfg.RequestTimeout,
	)

	gw, err := NewGateway(cfg)
	if err != nil {
		slog.Error("gateway: init error", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: gw,
		// ReadHeaderTimeout guards against slowloris-style attacks.
		ReadHeaderTimeout: 10 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("gateway: listen error", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("gateway: ready")
	<-quit

	slog.Info("gateway: shutting down — draining in-flight requests")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("gateway: shutdown error", "error", err)
	}
}
