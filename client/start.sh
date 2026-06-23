#!/bin/sh
set -e

: "${CLOUD_CONNECT_AUTH:?CLOUD_CONNECT_AUTH is required}"
: "${CLOUD_CONNECT_SERVER_URL:?CLOUD_CONNECT_SERVER_URL is required}"
: "${CLOUD_CONNECT_ALLOWED_HOST_PATTERN:?CLOUD_CONNECT_ALLOWED_HOST_PATTERN is required}"
: "${CLOUD_CONNECT_LOCAL_INGRESS:?CLOUD_CONNECT_LOCAL_INGRESS is required}"

envsubst '${CLOUD_CONNECT_ALLOWED_HOST_PATTERN} ${CLOUD_CONNECT_LOCAL_INGRESS}' \
    < /etc/nginx/nginx.conf.template \
    > /etc/nginx/nginx.conf

# Run nginx as daemon so chisel can be the foreground process.
# If the chisel connection drops the container exits and Kubernetes restarts the pod.
nginx

# R:9090:127.0.0.1:9091 instructs the server to bind port 9090 and tunnel
# incoming traffic to the nginx running on this pod at port 9091.
exec chisel client \
    --auth "${CLOUD_CONNECT_AUTH}" \
    "${CLOUD_CONNECT_SERVER_URL}" \
    "R:9090:127.0.0.1:9091"
