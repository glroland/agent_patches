// Optional OpenTelemetry trace export for the browser bundle.
//
// Unlike central-backend/endpoint-server, a static browser bundle can't read
// container environment variables at runtime, and we don't want to bake a
// collector address into a build artifact. So this module is always
// initialised and always exports to the fixed same-origin relative path
// `/v1/traces` — whether anything is actually listening there is controlled
// entirely server-side (nginx), via the same OTEL_EXPORTER_OTLP_ENDPOINT
// env var used by the other two components (see nginx.conf.template /
// entrypoint.sh). This also sidesteps CORS entirely, the same way `/api`
// and `/ws` already do.
//
// Export failures (nothing configured server-side, or the collector being
// unreachable) are handled internally by the SDK's batch pipeline via
// fetch — they never throw into application code.
import { registerInstrumentations } from '@opentelemetry/instrumentation';
import { FetchInstrumentation } from '@opentelemetry/instrumentation-fetch';
import { defaultResource, resourceFromAttributes } from '@opentelemetry/resources';
import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-http';
import { BatchSpanProcessor, WebTracerProvider } from '@opentelemetry/sdk-trace-web';

const TRACES_PATH = '/v1/traces';

const exporter = new OTLPTraceExporter({ url: TRACES_PATH });

const provider = new WebTracerProvider({
  resource: defaultResource().merge(
    resourceFromAttributes({ 'service.name': window.__OTEL_SERVICE_NAME__ || 'central-ui' })
  ),
  spanProcessors: [new BatchSpanProcessor(exporter)],
});

// Registers the global TracerProvider plus the default W3C TraceContext +
// Baggage propagator, so fetch() calls below inject the same `traceparent`
// header the other two components use to continue the trace.
provider.register();

registerInstrumentations({
  instrumentations: [
    new FetchInstrumentation({
      // Never trace the exporter's own export calls — fetch is instrumented
      // globally, and the exporter itself sends via fetch, so without this
      // every exported span would generate another span to export.
      ignoreUrls: [new RegExp(TRACES_PATH)],
    }),
  ],
});
