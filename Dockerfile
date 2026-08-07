# ──────────────────────────────────────────────
# Stage 1 — Build
# ──────────────────────────────────────────────
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache git ca-certificates tzdata make
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w -X main.version=1.0.0" -o /gyds-fullnode .

# ──────────────────────────────────────────────
# Stage 2 — Runtime
# ──────────────────────────────────────────────
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata curl
RUN addgroup -S gyds && adduser -S -G gyds gyds
WORKDIR /app
COPY --from=builder /gyds-fullnode /app/gyds-fullnode
RUN mkdir -p /app/data && chown -R gyds:gyds /app
USER gyds
VOLUME ["/app/data"]
ENV GYDS_CHAIN_ID=13370 \
    GYDS_NODE_MODE=full \
    GYDS_LOG_LEVEL=info \
    GYDS_DATA_DIR=/app/data \
    GYDS_DASHBOARD_PORT=5000 \
    GYDS_RPC_PORT=8545 \
    GYDS_RPC_HOST=0.0.0.0 \
    GYDS_P2P_PORT=30303
EXPOSE 5000 8545 8546 30303
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
  CMD curl -sf http://localhost:${GYDS_RPC_PORT}/health || exit 1
ENTRYPOINT ["/app/gyds-fullnode"]
CMD ["start"]
