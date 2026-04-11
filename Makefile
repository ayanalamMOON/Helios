.PHONY: all build clean test install docker changelog release release-rc release-beta

# Variables
BINARY_DIR=bin
CMD_DIR=cmd
ATLAS_BIN=$(BINARY_DIR)/helios-atlasd
GATEWAY_BIN=$(BINARY_DIR)/helios-gateway
PROXY_BIN=$(BINARY_DIR)/helios-proxy
WORKER_BIN=$(BINARY_DIR)/helios-worker
CLI_BIN=$(BINARY_DIR)/helios-cli
GENCERTS_BIN=$(BINARY_DIR)/helios-gencerts
VERSION?=
TARGET?=main
CHANNEL?=stable
PRERELEASE_ITERATION?=1
DRY_RUN?=false

# Default target
all: build

# Build all binaries
build: $(ATLAS_BIN) $(GATEWAY_BIN) $(PROXY_BIN) $(WORKER_BIN) $(CLI_BIN) $(GENCERTS_BIN)

$(BINARY_DIR):
	mkdir -p $(BINARY_DIR)

$(ATLAS_BIN): $(BINARY_DIR)
	go build -o $(ATLAS_BIN) ./$(CMD_DIR)/helios-atlasd

$(GATEWAY_BIN): $(BINARY_DIR)
	go build -o $(GATEWAY_BIN) ./$(CMD_DIR)/helios-gateway

$(PROXY_BIN): $(BINARY_DIR)
	go build -o $(PROXY_BIN) ./$(CMD_DIR)/helios-proxy

$(WORKER_BIN): $(BINARY_DIR)
	go build -o $(WORKER_BIN) ./$(CMD_DIR)/helios-worker

$(CLI_BIN): $(BINARY_DIR)
	go build -o $(CLI_BIN) ./$(CMD_DIR)/helios-cli

$(GENCERTS_BIN): $(BINARY_DIR)
	go build -o $(GENCERTS_BIN) ./$(CMD_DIR)/helios-gencerts

# Run tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Install binaries to system
install: build
	cp $(ATLAS_BIN) /usr/local/bin/
	cp $(GATEWAY_BIN) /usr/local/bin/
	cp $(PROXY_BIN) /usr/local/bin/
	cp $(WORKER_BIN) /usr/local/bin/
	cp $(CLI_BIN) /usr/local/bin/
	cp $(GENCERTS_BIN) /usr/local/bin/

# Generate TLS certificates for cluster
gencerts:
	$(GENCERTS_BIN) --output=./certs --nodes=node-1,node-2,node-3

# Clean build artifacts
clean:
	rm -rf $(BINARY_DIR)
	rm -f coverage.out coverage.html

# Build Docker images
docker:
	docker build -t helios/gateway:latest .
	docker build -t helios/atlas:latest -f Dockerfile.atlas .
	docker build -t helios/proxy:latest -f Dockerfile.proxy .
	docker build -t helios/worker:latest -f Dockerfile.worker .

# Run with Docker Compose
docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

# Run benchmarks
benchmark:
	./scripts/benchmark.sh

# Check AOF integrity
aof-check:
	node scripts/aof-check.js /var/lib/helios/appendonly.aof

# Generate documentation
docs:
	godoc -http=:6060

# Development: run all services locally
dev-atlas:
	go run ./$(CMD_DIR)/helios-atlasd --data-dir=./data --listen=:6379

dev-gateway:
	go run ./$(CMD_DIR)/helios-gateway --data-dir=./data --listen=:8443

dev-proxy:
	go run ./$(CMD_DIR)/helios-proxy --listen=:8080

dev-worker:
	go run ./$(CMD_DIR)/helios-worker --worker-id=dev-worker

# Help
help:
	@echo "HELIOS Build System"
	@echo ""
	@echo "Available targets:"
	@echo "  build          - Build all binaries"
	@echo "  test           - Run tests"
	@echo "  test-coverage  - Run tests with coverage"
	@echo "  install        - Install binaries to system"
	@echo "  clean          - Remove build artifacts"
	@echo "  docker         - Build Docker images"
	@echo "  docker-up      - Start services with Docker Compose"
	@echo "  docker-down    - Stop services"
	@echo "  fmt            - Format code"
	@echo "  lint           - Lint code"
	@echo "  benchmark      - Run benchmarks"
	@echo "  aof-check      - Validate AOF file"
	@echo "  docs           - Generate documentation"
	@echo "  changelog      - Generate release notes markdown (requires VERSION)"
	@echo "  release        - Create and push release tag (supports CHANNEL=stable|rc|beta)"
	@echo "  release-rc     - Shortcut for CHANNEL=rc"
	@echo "  release-beta   - Shortcut for CHANNEL=beta"
	@echo "  dev-*          - Run individual services in dev mode"

# Generate changelog/release notes for a tag
changelog:
	@if [ -z "$(VERSION)" ]; then \
		echo "Usage: make changelog VERSION=vX.Y.Z [PREVIOUS=vX.Y.Z]"; \
		exit 1; \
	fi
	@bash ./scripts/generate_changelog.sh "$(VERSION)" "$(PREVIOUS)"

# One-command release: create and push annotated tag (workflow publishes release)
release:
	@if [ -z "$(VERSION)" ]; then \
		echo "Usage: make release VERSION=vX.Y.Z [TARGET=main] [CHANNEL=stable|rc|beta] [PRERELEASE_ITERATION=1] [DRY_RUN=true|false]"; \
		exit 1; \
	fi
	@DRY_RUN=$(DRY_RUN) bash ./scripts/release.sh "$(VERSION)" "$(TARGET)" "$(CHANNEL)" "$(PRERELEASE_ITERATION)"

# Convenience target for release candidates
release-rc:
	@$(MAKE) release VERSION="$(VERSION)" TARGET="$(TARGET)" CHANNEL=rc PRERELEASE_ITERATION="$(PRERELEASE_ITERATION)" DRY_RUN="$(DRY_RUN)"

# Convenience target for beta releases
release-beta:
	@$(MAKE) release VERSION="$(VERSION)" TARGET="$(TARGET)" CHANNEL=beta PRERELEASE_ITERATION="$(PRERELEASE_ITERATION)" DRY_RUN="$(DRY_RUN)"
