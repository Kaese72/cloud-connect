# syntax=docker/dockerfile:1
FROM --platform=linux/amd64 golang:1.25-alpine AS chisel-builder
ARG CHISEL_VERSION=v1.10.1
RUN go install github.com/jpillora/chisel@${CHISEL_VERSION}

FROM --platform=linux/amd64 golang:1.25-alpine AS app-builder
WORKDIR /workspace
COPY . .
RUN CGO_ENABLED=0 go build -o cloud-connect-client

FROM alpine:latest
RUN apk add --no-cache nginx

COPY --from=chisel-builder /go/bin/chisel /usr/local/bin/chisel
COPY --from=app-builder /workspace/cloud-connect-client /usr/local/bin/cloud-connect-client
COPY nginx.conf.template /etc/nginx/nginx.conf.template

EXPOSE 8080
CMD ["/usr/local/bin/cloud-connect-client"]
