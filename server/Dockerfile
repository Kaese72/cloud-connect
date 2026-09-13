# syntax=docker/dockerfile:1
FROM --platform=linux/amd64 golang:1.25-alpine AS app-builder
WORKDIR /workspace
COPY . .
RUN CGO_ENABLED=0 go build -o cloud-connect-server

FROM alpine:latest
RUN apk add --no-cache nginx gettext

COPY --from=app-builder /workspace/cloud-connect-server /usr/local/bin/cloud-connect-server
COPY nginx.conf.template /etc/nginx/nginx.conf.template
COPY start.sh /start.sh
RUN chmod +x /start.sh

EXPOSE 8080
CMD ["/start.sh"]
