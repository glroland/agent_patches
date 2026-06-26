// Dev-time/build-time default. In the production container, entrypoint.sh
// regenerates this file at startup from the OTEL_SERVICE_NAME env var, so
// the value can be set per-deployment without rebuilding the image.
window.__OTEL_SERVICE_NAME__ = "central-ui";
