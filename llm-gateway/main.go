package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

	if cfg.AuthToken == "" {
		slog.Warn("gateway: GATEWAY_AUTH_TOKEN is not set — the gateway is open to unauthenticated requests; set this in production")
	}

	slog.Info("gateway: starting",
		"listen", cfg.ListenAddr,
		"upstream", cfg.UpstreamURL,
		"max_concurrency", cfg.MaxConcurrency,
		"max_queue_depth", cfg.MaxQueueDepth,
		"priority_queue_depth", cfg.PriorityQueueDepth,
		"request_timeout", cfg.RequestTimeout,
		"auth", cfg.AuthToken != "",
	)

	gw, err := NewGateway(cfg)
	if err != nil {
		slog.Error("gateway: init error", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: requireBearer(cfg.AuthToken, gw),
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

// requireBearer is an HTTP middleware that enforces bearer token authentication
// on all routes except GET /health, which must remain open for Kubernetes
// liveness and readiness probes. When token is empty the middleware is a
// no-op (all requests pass through), allowing unauthenticated deployments
// during development.
func requireBearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /health must always be reachable by Kubernetes probes.
		if r.Method == http.MethodGet && r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		hdr := r.Header.Get("Authorization")
		got, ok := strings.CutPrefix(hdr, "Bearer ")
		if !ok || got != token {
			slog.Warn("gateway: rejected unauthenticated request",
				"path", r.URL.Path,
				"remote", r.RemoteAddr,
			)
			http.Error(w, "gateway: unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
