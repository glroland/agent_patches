package tests

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"

	"agent_patches/endpoint-server/utils/tracing"
)

// When OTEL_EXPORTER_OTLP_ENDPOINT is unset — the default — Setup must not
// touch the global TracerProvider and must return a working no-op shutdown.
func TestTracingSetup_NoEndpoint_NoOp(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	before := otel.GetTracerProvider()

	shutdown := tracing.Setup(context.Background(), "test-service")

	if otel.GetTracerProvider() != before {
		t.Error("Setup changed the global TracerProvider despite no endpoint being configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Errorf("shutdown() = %v, want nil", err)
	}
}

// A configured-but-unreachable collector must not cause Setup or shutdown to
// fail — export errors are handled internally by the OTel batch pipeline and
// must never propagate into application startup/shutdown.
func TestTracingSetup_UnreachableEndpoint_DoesNotFail(t *testing.T) {
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })

	// Port 1 is reserved and nothing will ever accept a connection there.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")

	shutdown := tracing.Setup(context.Background(), "test-service")
	if shutdown == nil {
		t.Fatal("Setup returned a nil shutdown func")
	}

	// Record a span so Shutdown has something queued to actually attempt
	// exporting to the unreachable collector, exercising the failure path.
	_, span := otel.Tracer("tracing_test").Start(context.Background(), "test-span")
	span.End()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Errorf("shutdown() against an unreachable collector = %v, want nil (must not fail)", err)
	}
}
