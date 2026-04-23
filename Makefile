# Shark-MQTT Makefile

.PHONY: all test test-unit test-integration test-race test-bench test-coverage lint fmt build clean docker tidy verify ci

GO := go
GOFLAGS := -v

# Colors
GREEN  := $(shell tput setaf 2 2>/dev/null || echo '')
RED    := $(shell tput setaf 1 2>/dev/null || echo '')
YELLOW := $(shell tput setaf 3 2>/dev/null || echo '')
CYAN   := $(shell tput setaf 6 2>/dev/null || echo '')
RESET  := $(shell tput sgr0 2>/dev/null || echo '')

all: test build

# ─── Test Targets ────────────────────────────────────────

test: test-unit
	@echo "$(GREEN)[OK] All unit tests passed$(RESET)"

test-unit:
	@echo "$(CYAN)[TEST] Running unit tests...$(RESET)"
	$(GO) test $(GOFLAGS) -count=1 ./...

test-integration:
	@echo "$(CYAN)[TEST] Running integration tests...$(RESET)"
	$(GO) test $(GOFLAGS) -tags=integration -count=1 ./test/integration/...

test-redis:
	@echo "$(CYAN)[TEST] Running Redis store tests...$(RESET)"
	@if [ -z "$$MQTT_REDIS_ADDR" ]; then \
		echo "$(YELLOW)[WARN] MQTT_REDIS_ADDR not set, using localhost:6379$(RESET)"; \
		export MQTT_REDIS_ADDR=localhost:6379; \
	fi
	$(GO) test $(GOFLAGS) -v -count=1 ./store/redis/...

test-race:
	@echo "$(CYAN)[TEST] Running tests with race detector...$(RESET)"
	$(GO) test $(GOFLAGS) -race -count=1 ./...

test-bench:
	@echo "$(YELLOW)[BENCH] Running benchmarks...$(RESET)"
	$(GO) test -bench=. -benchmem -benchtime=5s -count=3 ./test/bench/...

test-coverage:
	@echo "$(CYAN)[TEST] Running tests with coverage...$(RESET)"
	$(GO) test -coverprofile=coverage.out -covermode=atomic -count=1 ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)Coverage report: coverage.html$(RESET)"
	@$(GO) tool cover -func=coverage.out | grep total

# ─── Code Quality Targets ─────────────────────────────────

lint:
	@echo "$(YELLOW)[LINT] Running linters...$(RESET)"
	$(GO) vet ./...

fmt:
	@echo "$(GREEN)[FMT] Formatting code...$(RESET)"
	$(GO) fmt ./...

# ─── Build Targets ────────────────────────────────────────

build:
	@echo "$(GREEN)[BUILD] Building...$(RESET)"
	$(GO) build $(GOFLAGS) ./...

build-example:
	@echo "$(GREEN)[BUILD] Building examples...$(RESET)"
	$(GO) build -o bin/shark-mqtt-example ./examples/...

# ─── Docker Targets ──────────────────────────────────────

docker-build:
	@echo "$(GREEN)[DOCKER] Building Docker image...$(RESET)"
	docker build -t shark-mqtt:latest .

docker-run:
	@echo "$(GREEN)[DOCKER] Running container...$(RESET)"
	docker run -p 1883:1883 -p 8883:8883 shark-mqtt:latest

# ─── Development Targets ─────────────────────────────────

tidy:
	$(GO) mod tidy

verify:
	$(GO) mod verify
	$(GO) build -v ./...

# ─── CI Simulation ───────────────────────────────────────

ci: fmt lint test-race build
	@echo "$(GREEN)[CI] All checks passed!$(RESET)"

# ─── Clean ───────────────────────────────────────────────

clean:
	@echo "$(GREEN)[CLEAN] Cleaning...$(RESET)"
	$(GO) clean
	rm -f coverage.out coverage.html
	rm -rf bin/
	docker rmi shark-mqtt:latest 2>/dev/null || true

# ─── Help ────────────────────────────────────────────────

help:
	@echo "Shark-MQTT Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make <target>"
	@echo ""
	@echo "Test targets:"
	@echo "  test              Run unit tests"
	@echo "  test-unit         Run unit tests"
	@echo "  test-integration  Run integration tests"
	@echo "  test-redis        Run Redis store tests"
	@echo "  test-race         Run tests with race detector"
	@echo "  test-bench        Run benchmarks"
	@echo "  test-coverage     Generate coverage report"
	@echo ""
	@echo "Code quality targets:"
	@echo "  lint              Run linters (go vet)"
	@echo "  fmt               Format code"
	@echo ""
	@echo "Build targets:"
	@echo "  build             Build all packages"
	@echo "  build-example     Build example binaries"
	@echo ""
	@echo "Docker targets:"
	@echo "  docker-build      Build Docker image"
	@echo "  docker-run        Run Docker container"
	@echo ""
	@echo "Development targets:"
	@echo "  tidy              Clean up go.mod"
	@echo "  verify            Verify dependencies"
	@echo "  ci                Run full CI pipeline"
	@echo ""
	@echo "Other targets:"
	@echo "  clean             Clean build artifacts"
	@echo "  help              Show this help"
