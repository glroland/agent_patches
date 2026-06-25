// Optional OpenTelemetry trace export, configured entirely through the
// standard OTEL_* environment variables so the same configuration applies
// uniformly across central-ui, central-backend, and endpoint-server. Tracing
// stays fully inert until OTEL_EXPORTER_OTLP_ENDPOINT is set; once started,
// export failures are handled internally by the SDK's batch pipeline and
// never propagate into the request path.
//
// Imported from setup.js (loaded via `node --import`) so the SDK starts —
// and patches http/express/undici — before any instrumented module is
// required by src/index.js.

const endpoint = process.env.OTEL_EXPORTER_OTLP_ENDPOINT;

if (!endpoint) {
  // No special-casing needed elsewhere: leaving the SDK unstarted means
  // every API call site (e.g. auto-instrumented http/express/undici) is
  // already a no-op via the OpenTelemetry API's own default behavior.
} else {
  const { NodeSDK } = await import('@opentelemetry/sdk-node');
  const { OTLPTraceExporter } = await import('@opentelemetry/exporter-trace-otlp-http');
  const { getNodeAutoInstrumentations } = await import('@opentelemetry/auto-instrumentations-node');

  const sdk = new NodeSDK({
    serviceName: process.env.OTEL_SERVICE_NAME || 'central-backend',
    traceExporter: new OTLPTraceExporter(),
    // Covers http, express, and undici (Node's built-in fetch, used by
    // services/agentClient.js) — outgoing calls to each endpoint-server
    // agent automatically get a client span and an injected traceparent
    // header that continues the trace into that agent's process.
    instrumentations: [getNodeAutoInstrumentations()],
  });

  sdk.start();
  // eslint-disable-next-line no-console
  console.log(`[tracing] OpenTelemetry export enabled (endpoint=${endpoint})`);

  const shutdown = () => {
    sdk.shutdown().catch(() => {
      // Shutdown failures (e.g. unreachable collector during final flush)
      // must not prevent the process from exiting.
    });
  };
  process.once('SIGTERM', shutdown);
  process.once('SIGINT', shutdown);
}
