// Package tracing provides optional OpenTelemetry trace export, configured
// entirely through the standard OTEL_* environment variables so the same
// configuration applies uniformly across central-ui, central-backend, and
// endpoint-server. Tracing stays fully inert until OTEL_EXPORTER_OTLP_ENDPOINT
// is set; once a global TracerProvider is installed, export failures are
// handled internally by the SDK's batch pipeline and never propagate into
// the request path.
package tracing

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

const (
	endpointEnvKey   = "OTEL_EXPORTER_OTLP_ENDPOINT"
	samplerEnvKey    = "OTEL_TRACES_SAMPLER"
	samplerArgEnvKey = "OTEL_TRACES_SAMPLER_ARG"

	shutdownTimeout = 5 * time.Second
)

// Setup configures global OpenTelemetry trace export when
// OTEL_EXPORTER_OTLP_ENDPOINT is set, using defaultServiceName as the
// service.name resource attribute (overridden by OTEL_SERVICE_NAME when
// set). It returns a shutdown function that flushes and closes the exporter
// with a bounded timeout.
//
// When the endpoint is unset — the default — Setup does nothing and returns
// a no-op shutdown. The global TracerProvider is left untouched, so every
// otel.Tracer(...)/otelhttp call elsewhere in the app is already a no-op via
// the OTel SDK's own default behavior; no special-casing is needed in
// business logic.
func Setup(ctx context.Context, defaultServiceName string) (shutdown func(context.Context) error) {
	noop := func(context.Context) error { return nil }

	endpoint := strings.TrimSpace(os.Getenv(endpointEnvKey))
	if endpoint == "" {
		slog.Debug("tracing: OTEL_EXPORTER_OTLP_ENDPOINT not set, tracing disabled")
		return noop
	}

	// otlptracehttp.New reads OTEL_EXPORTER_OTLP_ENDPOINT/_HEADERS itself.
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		slog.Warn("tracing: failed to create OTLP exporter, tracing disabled", "error", err)
		return noop
	}

	// resource.WithFromEnv reads OTEL_SERVICE_NAME/OTEL_RESOURCE_ATTRIBUTES
	// and, when set, overrides the default attribute supplied here.
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceNameKey.String(defaultServiceName)),
		resource.WithFromEnv(),
	)
	if err != nil {
		slog.Warn("tracing: resource detection partially failed, continuing", "error", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(samplerFromEnv()),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	slog.Info("tracing: OpenTelemetry export enabled", "endpoint", endpoint, "service_name", defaultServiceName)

	return func(shutdownCtx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(shutdownCtx, shutdownTimeout)
		defer cancel()
		return tp.Shutdown(shutdownCtx)
	}
}

// samplerFromEnv builds a Sampler from OTEL_TRACES_SAMPLER /
// OTEL_TRACES_SAMPLER_ARG — the one piece of standard OTel configuration the
// Go SDK does not read automatically (Node's NodeSDK does this natively;
// see central-backend/src/tracing.js). Defaults to parentbased_always_on,
// the same default used by the other OTel SDKs, on any unset or
// unrecognised value.
func samplerFromEnv() sdktrace.Sampler {
	name := strings.ToLower(strings.TrimSpace(os.Getenv(samplerEnvKey)))
	arg := strings.TrimSpace(os.Getenv(samplerArgEnvKey))

	ratio := func() float64 {
		r, err := strconv.ParseFloat(arg, 64)
		if err != nil || r < 0 || r > 1 {
			slog.Warn("tracing: invalid OTEL_TRACES_SAMPLER_ARG, defaulting ratio to 1.0", "value", arg)
			return 1
		}
		return r
	}

	switch name {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(ratio())
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case "parentbased_traceidratio":
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio()))
	case "", "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	default:
		slog.Warn("tracing: unrecognised OTEL_TRACES_SAMPLER, defaulting to parentbased_always_on", "value", name)
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
}
