#!/bin/bash

current_dir=$(pwd)
echo "Current Working Directory: $current_dir"

echo "Backend URL: $BACKEND_URL"

# In-cluster ClusterIP address of central-backend, used only by the
# /health/ready location so readiness checks stay inside the cluster instead
# of round-tripping through the external Route. Defaults to the Helm chart's
# service name so this still works if the var is left unset.
export BACKEND_INTERNAL_URL="${BACKEND_INTERNAL_URL:-http://central-backend:8080}"
echo "Backend internal URL: $BACKEND_INTERNAL_URL"

# How long nginx waits on a response from central-backend for /api requests
# (agent chat, tool-use loops with operator approval can take minutes). Must
# stay below the central-ui Route's own haproxy timeout (route.timeout) or
# nginx will cut the connection before the Route would.
export PROXY_READ_TIMEOUT="${PROXY_READ_TIMEOUT:-60s}"
echo "Proxy read timeout: $PROXY_READ_TIMEOUT"

# Same env var name used by central-backend/endpoint-server. Default to a
# loopback placeholder when unset so nginx always has a syntactically valid
# proxy_pass target for /v1/traces — tracing simply stays unreachable
# (connection refused), which the browser exporter already treats as a
# non-fatal export failure, rather than nginx failing to start.
export OTEL_EXPORTER_OTLP_ENDPOINT="${OTEL_EXPORTER_OTLP_ENDPOINT:-http://127.0.0.1:4318}"
echo "OTel collector endpoint: $OTEL_EXPORTER_OTLP_ENDPOINT"

# Service name is per-component, unlike the shared collector endpoint above,
# so it's not part of the nginx proxy — it just needs to reach the browser
# bundle. Regenerate the runtime config the bundle reads at startup so it's
# configurable per-deployment without rebuilding the image.
export OTEL_SERVICE_NAME="${OTEL_SERVICE_NAME:-central-ui}"
echo "OTel service name: $OTEL_SERVICE_NAME"
cat > /usr/share/nginx/html/otel-config.js <<EOF
window.__OTEL_SERVICE_NAME__ = "${OTEL_SERVICE_NAME}";
EOF

echo "Substituting BACKEND_URL, BACKEND_INTERNAL_URL, OTEL_EXPORTER_OTLP_ENDPOINT and PROXY_READ_TIMEOUT variables into /tmp/nginx-custom.conf"
envsubst '$BACKEND_URL:$BACKEND_INTERNAL_URL:$OTEL_EXPORTER_OTLP_ENDPOINT:$PROXY_READ_TIMEOUT' < /nginx.conf.template > /tmp/nginx-custom.conf

echo "Starting nginx"
nginx -g 'daemon off;' -c /tmp/nginx-custom.conf

echo "Exiting"

