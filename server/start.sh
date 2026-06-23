#!/bin/sh
set -e

: "${CLOUD_CONNECT_INTERNAL_HOST:?CLOUD_CONNECT_INTERNAL_HOST is required}"
: "${CLOUD_CONNECT_AUTH:?CLOUD_CONNECT_AUTH is required}"

envsubst '${CLOUD_CONNECT_INTERNAL_HOST}' \
    < /etc/nginx/nginx.conf.template \
    > /etc/nginx/nginx.conf

# Run nginx as daemon so chisel can be the foreground process.
# If chisel exits the container exits and Kubernetes restarts the pod.
nginx

exec chisel server \
    --port 8081 \
    --auth "${CLOUD_CONNECT_AUTH}" \
    --reverse
