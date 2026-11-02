# syntax=docker/dockerfile:1
# ProxyPilot Go 服务镜像（构建上下文 = 仓库根目录）
FROM golang:1.23-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.version=V15.0" \
    -o /out/proxypilot ./cmd/proxypilot

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget \
    && adduser -D -u 10001 -h /app app
WORKDIR /app
COPY --from=build /out/proxypilot /usr/local/bin/proxypilot
RUN mkdir -p /app/data/bucket /app/agent-dist && chown -R app:app /app
USER app
ENV PP_ADDR=:8080 \
    PP_AGENT_DIST_DIR=/app/agent-dist \
    PP_BUCKET_DIR=/app/data/bucket
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --retries=5 CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/proxypilot"]
