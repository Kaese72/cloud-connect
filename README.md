# Cloud Connect

This service handles back-connect from **the cloud** into the place
where a Huemie appliance is running.

## Overview

Cloud Connect exposes the local Huemie cluster to the internet without opening inbound firewall ports. A local pod establishes an outbound reverse tunnel to a cloud endpoint; inbound public traffic flows through that tunnel to the local ingress.

Encryption is handled by the tunnel layer ([chisel](https://github.com/jpillora/chisel)). HTTP inside the tunnel is plain (not TLS), which simplifies the local setup.

## Architecture

```
Internet
    │
    ▼
Cloud Ingress / LoadBalancer  (single public hostname)
    │
    ▼
nginx  (cloud pod)
    ├── POST /cloud-connect/v0/tunnel  (WebSocket upgrade)
    │       │
    │       ▼
    │   chisel server  (localhost:8080, cloud pod)
    │       │  reverse tunnel (outbound from local)
    │       ▼
    │   chisel client  (local pod)
    │
    └── /*  (all other paths)
            │  nginx rewrites Host header to internal DNS name
            ▼
        localhost:<tunnel-bound-port>  (chisel reverse tunnel entry)
            │  through tunnel
            ▼
        local nginx  (local pod)
            │  enforces Host header whitelist
            ▼
        local k8s Ingress
            │
            ▼
        internal services
```

## Components

### Cloud pod

Two processes in a single container:

| Process | Role |
|---|---|
| **chisel server** | Accepts the reverse tunnel WebSocket connection from the local pod on `localhost:8080`. Binds a localhost port (`<tunnel-bound-port>`) where tunneled traffic emerges. |
| **nginx** | Single public entry point. Routes `/cloud-connect/v0/tunnel` (WebSocket) to the chisel server. Routes all other traffic through the tunnel port after rewriting the `Host` header to the target internal DNS name. |

### Local pod

Two processes in a single container:

| Process | Role |
|---|---|
| **chisel client** | Connects outbound to the cloud endpoint at `/cloud-connect/v0/tunnel`, authenticates with a shared secret, and establishes the reverse tunnel. |
| **nginx** | Receives traffic arriving through the tunnel. Validates the `Host` header against a whitelist of permitted internal hostnames. Forwards to the local k8s ingress. |

## Tunnel endpoint

The chisel client connects to:

```
wss://<cloud-hostname>/cloud-connect/v0/tunnel
```

The `/cloud-connect/v0/tunnel` path is routed by the cloud nginx to the chisel server via a WebSocket proxy. All other paths are treated as user traffic flowing through the reverse tunnel.

## Authentication

The chisel client authenticates to the chisel server using a **shared secret** configured via environment variable. The server rejects connections that do not present the correct secret.

The secret is not managed by kustomize and must be created manually in each cluster before the service is deployed. The `auth` value must be identical in both namespaces.

**Cloud cluster (`huemie-cloud` namespace):**
```sh
kubectl create secret generic cloud-connect-secret \
  --from-literal=auth=user:yourpassword \
  -n huemie-cloud
```

**Appliance cluster (`huemie-local` namespace):**
```sh
kubectl create secret generic cloud-connect-secret \
  --from-literal=auth=user:yourpassword \
  -n huemie-local
```

## Host header flow

1. Public request arrives at the cloud nginx with an external `Host` header.
2. Cloud nginx rewrites `Host` to the target internal DNS name before forwarding through the tunnel.
3. Local nginx validates the `Host` header against a static whitelist. Requests with unrecognised hostnames are rejected (HTTP 421).
4. Accepted requests are forwarded to the local k8s ingress, which routes based on the `Host` header as normal.

## Security properties

- No inbound ports opened on the local network; the tunnel is outbound-only from local.
- The shared secret prevents unauthorised tunnel establishment.
- The local nginx Host whitelist prevents the tunnel from being used to reach arbitrary internal services.
- TLS termination is handled by the cloud ingress; the tunnel itself is encrypted by chisel (WebSocket over TLS).
