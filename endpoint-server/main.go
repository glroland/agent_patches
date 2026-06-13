package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"agent_patches/endpoint-server/a2a/agent"
	"agent_patches/endpoint-server/a2a/executor"
	tasks "agent_patches/endpoint-server/a2a/registry"
	"agent_patches/endpoint-server/loop"
	"agent_patches/endpoint-server/memory"
	"agent_patches/endpoint-server/skills/analyze_disk_utilization"
	"agent_patches/endpoint-server/skills/analyze_memory_utilization"
	"agent_patches/endpoint-server/skills/analyze_network_utilization"
	"agent_patches/endpoint-server/skills/capture_system_info"
	"agent_patches/endpoint-server/skills/check_for_pending_system_patches"
	"agent_patches/endpoint-server/skills/check_interactive_logins"
	"agent_patches/endpoint-server/skills/ping"
	"agent_patches/endpoint-server/skills/read_agent_memory"
	"agent_patches/endpoint-server/utils/config"
	"agent_patches/endpoint-server/utils/logger"
	"agent_patches/endpoint-server/utils/notifier"
	"agent_patches/endpoint-server/utils/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err,
			"hint", fmt.Sprintf("copy config.example.yaml to config.yaml or set %s", config.EnvKey))
		return
	}

	if err := logger.Setup(cfg.Logging.Level, cfg.Logging.File); err != nil {
		slog.Error("failed to open log file", "error", err)
		return
	}
	slog.Info("agent_patches starting",
		"model", cfg.Agent.Model,
		"security", cfg.Security.Scheme,
		"tasks_file", cfg.Storage.TasksFile,
	)

	store := storage.NewStore(cfg.Storage.TasksFile)
	notify := notifier.New(&cfg.Notifier)

	registry := tasks.NewRegistry()

	pingTool, err := ping.NewPingTool()
	if err != nil {
		slog.Error("failed to create ping tool", "error", err)
		return
	}
	registry.Register(pingTool)

	patchTool, err := check_for_pending_system_patches.NewPatchTool(notify)
	if err != nil {
		slog.Error("failed to create check_for_pending_system_patches tool", "error", err)
		return
	}
	registry.Register(patchTool)

	diskUsageTool, err := analyze_disk_utilization.NewDiskUsageTool()
	if err != nil {
		slog.Error("failed to create analyze_disk_utilization tool", "error", err)
		return
	}
	registry.Register(diskUsageTool)

	memoryUsageTool, err := analyze_memory_utilization.NewMemoryUsageTool()
	if err != nil {
		slog.Error("failed to create analyze_memory_utilization tool", "error", err)
		return
	}
	registry.Register(memoryUsageTool)

	networkUsageTool, err := analyze_network_utilization.NewNetworkUsageTool()
	if err != nil {
		slog.Error("failed to create analyze_network_utilization tool", "error", err)
		return
	}
	registry.Register(networkUsageTool)

	loginSessionsTool, err := check_interactive_logins.NewLoginSessionsTool()
	if err != nil {
		slog.Error("failed to create check_interactive_logins tool", "error", err)
		return
	}
	registry.Register(loginSessionsTool)

	mem := memory.New(&cfg.Memory)

	readMemoryTool, err := read_agent_memory.NewReadMemoryTool(mem)
	if err != nil {
		slog.Error("failed to create read_agent_memory tool", "error", err)
		return
	}
	registry.Register(readMemoryTool)

	systemInfoTool, err := capture_system_info.NewSystemInfoTool()
	if err != nil {
		slog.Error("failed to create capture_system_info tool", "error", err)
		return
	}
	registry.Register(systemInfoTool)

	if report, err := systemInfoTool.Execute(context.Background(), json.RawMessage("{}")); err != nil {
		slog.Error("capture_system_info: failed to gather host metadata", "error", err)
	} else {
		slog.Info("capture_system_info: gathered host metadata for responsibility system prompt", "report", report)
		cfg.ResponsibilitySystemPrompt = cfg.ResponsibilitySystemPrompt + "\n\nHost metadata:\n" + report
	}

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
	cardURL := cfg.Server.PublicURL
	if cardURL == "" {
		host := cfg.Server.Host
		if host == "0.0.0.0" || host == "" {
			if h, err := os.Hostname(); err == nil {
				host = h
			} else {
				host = "localhost"
			}
		}
		cardURL = fmt.Sprintf("http://%s:%d", host, cfg.Server.Port)
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

	loop.New(cfg, registry, notify).Start(ctx)

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
