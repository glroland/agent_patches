#!/bin/bash

current_dir=$(pwd)
echo "Current Working Directory: $current_dir"

echo "Backend URL: $BACKEND_URL"

echo "Substituting BACKEND_URL variable into /tmp/nginx-custom.conf"
envsubst '$BACKEND_URL' < /nginx.conf.template > /tmp/nginx-custom.conf

echo "Starting nginx"
nginx -g 'daemon off;' -c /tmp/nginx-custom.conf

echo "Exiting"

