# Dockerfile for HELIOS Gateway
FROM golang:1.21 AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum* ./
RUN go mod download || true

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o helios-gateway ./cmd/helios-gateway

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/helios-gateway .

# Copy default config
COPY configs/default.yaml /etc/helios/config.yaml

# Create data directory
RUN mkdir -p /var/lib/helios

# Expose ports
EXPOSE 8443

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8443/health || exit 1

VOLUME ["/var/lib/helios"]

CMD ["./helios-gateway", "--data-dir=/var/lib/helios", "--listen=:8443"]
