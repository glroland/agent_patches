#!/bin/bash

current_dir=$(pwd)
echo "Current Working Directory: $current_dir"

echo "Backend URL: $BACKEND_URL"

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

echo "Substituting BACKEND_URL and OTEL_EXPORTER_OTLP_ENDPOINT variables into /tmp/nginx-custom.conf"
envsubst '$BACKEND_URL:$OTEL_EXPORTER_OTLP_ENDPOINT' < /nginx.conf.template > /tmp/nginx-custom.conf

echo "Starting nginx"
nginx -g 'daemon off;' -c /tmp/nginx-custom.conf

echo "Exiting"

