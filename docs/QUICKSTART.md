# HELIOS Quick Start Guide

## Prerequisites

- Go 1.21 or later
- Git (optional)

## Building

```bash
# Navigate to project directory
cd c:/Users/ayana/Projects/Helios

# Download dependencies
go mod tidy

# Build all binaries
make build

# Or build individually:
go build -o bin/helios-atlasd.exe ./cmd/helios-atlasd
go build -o bin/helios-gateway.exe ./cmd/helios-gateway
go build -o bin/helios-proxy.exe ./cmd/helios-proxy
go build -o bin/helios-worker.exe ./cmd/helios-worker
```

## Running Components

### 1. Start ATLAS Daemon (Standalone Mode)

```bash
./bin/helios-atlasd.exe --data-dir=./data --listen=:6379
```

Expected output:
```json
{"timestamp":"2026-01-29T...","level":"INFO","component":"atlasd","message":"Starting ATLAS daemon"}
{"timestamp":"2026-01-29T...","level":"INFO","component":"atlasd","message":"ATLAS instance created successfully"}
ATLAS server listening on :6379
{"timestamp":"2026-01-29T...","level":"INFO","component":"atlasd","message":"ATLAS daemon is running"}
```

### 2. Start API Gateway (Embedded ATLAS)

```bash
./bin/helios-gateway.exe --listen=:8443 --data-dir=./data
```

Expected output:
```json
{"timestamp":"2026-01-29T...","level":"INFO","component":"gateway","message":"Starting API Gateway"}
{"timestamp":"2026-01-29T...","level":"INFO","component":"gateway","message":"API Gateway is running"}
API Gateway listening on :8443
```

### 3. Start Reverse Proxy (Optional)

```bash
./bin/helios-proxy.exe --listen=:8080
```

### 4. Start Worker (Optional)

```bash
./bin/helios-worker.exe --worker-id=worker-1 --poll-interval=5s
```

## Testing

### Run Unit Tests

```bash
# All tests
go test ./...

# With coverage
go test -cover ./...

# Verbose output
go test -v ./...

# Specific package
go test -v ./internal/atlas/store/
go test -v ./internal/atlas/protocol/
```

### Test ATLAS Manually

Connect using netcat or telnet:

```bash
# Using echo and netcat (if available)
echo '{"cmd":"SET","key":"test","value":"hello","ttl":0}' | nc localhost 6379
echo '{"cmd":"GET","key":"test"}' | nc localhost 6379
```

### Test API Gateway

```bash
# Register a user
curl -X POST http://localhost:8443/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"testuser","password":"testpass123"}'

# Login
curl -X POST http://localhost:8443/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"testuser","password":"testpass123"}'

# Use the returned token for authenticated requests
TOKEN="your-token-here"

# Enqueue a job
curl -X POST http://localhost:8443/api/v1/jobs \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"payload":{"task":"process","data":"test"}}'
```

## Configuration

Edit `configs/default.yaml` to customize settings:

```yaml
atlas:
  data_dir: "/var/lib/helios"
  aof_fsync: "every"  # Options: every, interval, none

gateway:
  listen: ":8443"
  rate_limit:
    default_capacity: 100
    default_rate_num: 1
    default_rate_den: 1
```

## Common Issues

### Port Already in Use

```bash
# Change the port in the command
./bin/helios-gateway.exe --listen=:8444

# Or stop the conflicting service
netstat -ano | findstr :8443
taskkill /PID <pid> /F
```

### Permission Denied on Data Directory

```bash
# Ensure the data directory exists and is writable
mkdir -p ./data
```

### Dependencies Not Found

```bash
# Re-download dependencies
go mod tidy
```

## Development

### Run in Development Mode

```bash
# ATLAS daemon
make dev-atlas

# API Gateway
make dev-gateway

# Worker
make dev-worker
```

### Format Code

```bash
make fmt
```

### Clean Build Artifacts

```bash
make clean
```

## Docker (Optional)

### Build Docker Image

```bash
docker build -t helios/gateway:latest .
```

### Run with Docker Compose

```bash
docker-compose up -d
```

## Verification

Check that everything is working:

```bash
# Check binaries exist
ls -lh bin/

# Verify no compilation errors
go build ./...

# Run tests
go test ./...

# Check for any remaining errors
go vet ./...
```

## Next Steps

1. Read the [Architecture Documentation](docs/architecture.md)
2. Explore the API endpoints in the code
3. Customize configuration for your use case
4. Add your own endpoints and handlers
5. Deploy to your environment

## Getting Help

- Check [TEST_REPORT.md](TEST_REPORT.md) for detailed test results
- Read [README.md](README.md) for complete documentation
- Review [docs/architecture.md](docs/architecture.md) for system design

## Success Indicators

✅ All binaries compile without errors
✅ Tests pass with 82%+ coverage
✅ Services start and log successfully
✅ No compilation or import errors

Your HELIOS framework is ready to use!
