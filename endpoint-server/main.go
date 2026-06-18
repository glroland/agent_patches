package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	"agent_patches/endpoint-server/memoryapi"
	"agent_patches/endpoint-server/skills/analyze_memory_utilization"
	"agent_patches/endpoint-server/skills/analyze_network_utilization"
	"agent_patches/endpoint-server/skills/capture_system_info"
	"agent_patches/endpoint-server/skills/check_drives"
	"agent_patches/endpoint-server/skills/check_for_pending_system_patches"
	"agent_patches/endpoint-server/skills/check_interactive_logins"
	"agent_patches/endpoint-server/skills/ping"
	"agent_patches/endpoint-server/skills/read_agent_memory"
	"agent_patches/endpoint-server/skills/report_findings"
	"agent_patches/endpoint-server/status"
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
	mem := memory.New(&cfg.Memory)
	notify := notifier.New(mem)

	registry := tasks.NewRegistry()

	pingTool, err := ping.NewPingTool()
	if err != nil {
		slog.Error("failed to create ping tool", "error", err)
		return
	}
	registry.Register(pingTool)

	patchTool, err := check_for_pending_system_patches.NewPatchTool(notify, mem)
	if err != nil {
		slog.Error("failed to create check_for_pending_system_patches tool", "error", err)
		return
	}
	registry.Register(patchTool)

	diskUsageTool, err := check_drives.NewDiskUsageTool(mem)
	if err != nil {
		slog.Error("failed to create check_drives tool", "error", err)
		return
	}
	registry.Register(diskUsageTool)

	memoryUsageTool, err := analyze_memory_utilization.NewMemoryUsageTool(mem)
	if err != nil {
		slog.Error("failed to create analyze_memory_utilization tool", "error", err)
		return
	}
	registry.Register(memoryUsageTool)

	networkUsageTool, err := analyze_network_utilization.NewNetworkUsageTool(mem)
	if err != nil {
		slog.Error("failed to create analyze_network_utilization tool", "error", err)
		return
	}
	registry.Register(networkUsageTool)

	loginSessionsTool, err := check_interactive_logins.NewLoginSessionsTool(mem)
	if err != nil {
		slog.Error("failed to create check_interactive_logins tool", "error", err)
		return
	}
	registry.Register(loginSessionsTool)

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

	reportFindingsTool, err := report_findings.NewReportFindingsTool(mem)
	if err != nil {
		slog.Error("failed to create report_findings tool", "error", err)
		return
	}
	registry.Register(reportFindingsTool)

	hostInfo, err := capture_system_info.Gather()
	if err != nil {
		slog.Error("capture_system_info: failed to gather host metadata", "error", err)
	} else {
		report := capture_system_info.BuildReport(hostInfo)
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
			if fqdn, ok := lookupFQDN(); ok {
				host = fqdn
			} else if h, err := os.Hostname(); err == nil {
				host = h
			} else {
				host = "localhost"
			}
		}
		cardURL = fmt.Sprintf("http://%s:%d", host, cfg.Server.Port)
	}

	card := buildAgentCard(cardURL, cfg, registry)

	lp := loop.New(cfg, registry, notify)
	statusSvc := status.New(hostInfo, mem, lp)
	memorySvc := memoryapi.New(mem)

	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	var statusHandler http.Handler = statusSvc.Handler()
	var memoryHandler http.Handler = memorySvc.Handler()
	if cfg.Security.Scheme == "bearer" {
		statusHandler = requireBearer(cfg.Security.Token, statusHandler)
		memoryHandler = requireBearer(cfg.Security.Token, memoryHandler)
	}
	mux.Handle("/status", statusHandler)
	mux.Handle("/memory", memoryHandler)
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

	// Propagate the signal context into every in-flight HTTP request so that
	// handlers (and the tools they call) abort promptly on SIGTERM/SIGINT.
	srv.BaseContext = func(_ net.Listener) context.Context { return ctx }

	lp.Start(ctx)

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

// requireBearer wraps an http.Handler so requests must carry a matching
// "Authorization: Bearer <token>" header, returning 401 otherwise. Used to
// protect plain HTTP endpoints (e.g. /status) outside the JSON-RPC handler,
// which authenticates via bearerInterceptor instead.
func requireBearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || got != token {
			slog.Warn("auth: missing or invalid bearer token", "path", r.URL.Path)
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
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

// lookupFQDN tries to determine this host's fully-qualified domain name via
// reverse DNS: resolve the local hostname to an IP, then look up the PTR
// record for that IP. Returns ok=false if the hostname can't be resolved or
// no PTR record is found.
func lookupFQDN() (string, bool) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", false
	}
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		return "", false
	}
	for _, addr := range addrs {
		names, err := net.LookupAddr(addr)
		if err != nil || len(names) == 0 {
			continue
		}
		return strings.TrimSuffix(names[0], "."), true
	}
	return "", false
}
