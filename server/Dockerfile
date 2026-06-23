# syntax=docker/dockerfile:1
FROM --platform=linux/amd64 golang:1.25-alpine AS chisel-builder
ARG CHISEL_VERSION=v1.10.1
RUN go install github.com/jpillora/chisel@${CHISEL_VERSION}

FROM alpine:latest
RUN apk add --no-cache nginx gettext

COPY --from=chisel-builder /go/bin/chisel /usr/local/bin/chisel
COPY nginx.conf.template /etc/nginx/nginx.conf.template
COPY start.sh /start.sh
RUN chmod +x /start.sh

EXPOSE 8080
CMD ["/start.sh"]
