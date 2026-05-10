package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"agent_patches/server/agent"
	"agent_patches/server/config"
	"agent_patches/server/executor"
	"agent_patches/server/logger"
	"agent_patches/server/storage"
	"agent_patches/server/tasks"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err,
			"hint", fmt.Sprintf("copy config.example.yaml to config.yaml or set %s", config.EnvKey))
		return
	}

	logger.Setup(cfg.Logging.Level)
	slog.Info("agent_patches starting",
		"model", cfg.Agent.Model,
		"security", cfg.Security.Scheme,
		"tasks_file", cfg.Storage.TasksFile,
	)

	store := storage.NewStore(cfg.Storage.TasksFile)

	registry := tasks.NewRegistry()

	helloTool, err := tasks.NewHelloTool()
	if err != nil {
		slog.Error("failed to create hello tool", "error", err)
		return
	}
	registry.Register(helloTool)

	patchTool, err := tasks.NewPatchTool()
	if err != nil {
		slog.Error("failed to create patch tool", "error", err)
		return
	}
	registry.Register(patchTool)

	a := agent.New(storage.WrapAll(registry.Tools(), store), cfg)
	exec := executor.New(a)

	handlerOpts := []a2asrv.RequestHandlerOption{
		a2asrv.WithCallInterceptors(a2asrv.NewLoggingInterceptor(nil)),
	}
	if cfg.Security.Scheme == "bearer" {
		handlerOpts = append(handlerOpts, a2asrv.WithCallInterceptors(newBearerInterceptor(cfg.Security.Token)))
	}

	reqHandler := a2asrv.NewHandler(exec, handlerOpts...)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	cardURL := fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	if cfg.Server.Host == "0.0.0.0" || cfg.Server.Host == "" {
		cardURL = fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
	}

	card := buildAgentCard(cardURL, cfg, registry)

	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/", a2asrv.NewJSONRPCHandler(reqHandler))

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("server listening", "addr", addr, "card", cardURL+a2asrv.WellKnownAgentCardPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("stopped")
}

func buildAgentCard(url string, cfg *config.Settings, registry *tasks.Registry) *a2a.AgentCard {
	skills := make([]a2a.AgentSkill, 0, len(registry.Tools()))
	for _, t := range registry.Tools() {
		skills = append(skills, a2a.AgentSkill{
			ID:          t.Name(),
			Name:        t.Name(),
			Description: t.Description(),
			Tags:        []string{"server-admin"},
		})
	}

	card := &a2a.AgentCard{
		Name:        "agent_patches",
		Description: "Server administration AI agent",
		Version:     "1.0.0",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(url, a2a.TransportProtocolJSONRPC),
		},
		Skills: skills,
	}

	if cfg.Security.Scheme == "bearer" {
		card.SecuritySchemes = a2a.NamedSecuritySchemes{
			"bearer": a2a.HTTPAuthSecurityScheme{Scheme: "bearer"},
		}
		card.SecurityRequirements = a2a.SecurityRequirementsOptions{
			a2a.SecurityRequirements{"bearer": nil},
		}
	}

	return card
}

// bearerInterceptor validates Bearer tokens on every JSON-RPC request.
type bearerInterceptor struct {
	a2asrv.PassthroughCallInterceptor
	token string
}

func newBearerInterceptor(token string) *bearerInterceptor {
	slog.Info("security: bearer authentication enabled")
	return &bearerInterceptor{token: token}
}

func (b *bearerInterceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, _ *a2asrv.Request) (context.Context, any, error) {
	vals, ok := callCtx.ServiceParams().Get("authorization")
	if !ok || len(vals) == 0 {
		slog.Warn("auth: missing Authorization header", "method", callCtx.Method())
		return ctx, nil, a2a.ErrUnauthenticated
	}
	token, ok := strings.CutPrefix(vals[0], "Bearer ")
	if !ok || token != b.token {
		slog.Warn("auth: invalid token", "method", callCtx.Method())
		return ctx, nil, a2a.ErrUnauthenticated
	}
	return ctx, nil, nil
}
