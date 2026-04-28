# Shark-MQTT Makefile

.PHONY: all test test-unit test-integration test-race test-bench bench bench-quick bench-cpu bench-mem bench-profile test-coverage lint fmt build clean docker docker-build docker-run docker-test docker-compose-up docker-compose-down k8s-deploy k8s-delete tidy verify ci

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
	$(GO) test $(GOFLAGS) -tags=integration -count=1 ./tests/integration/...

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
	$(GO) test -bench=. -benchmem -benchtime=5s -count=3 ./tests/bench/...

bench: test-bench

bench-quick:
	@echo "$(YELLOW)[BENCH] Quick benchmark (1s per test)...$(RESET)"
	$(GO) test -bench=. -benchmem -benchtime=1s -count=1 ./tests/bench/...

bench-cpu:
	@echo "$(YELLOW)[BENCH] CPU profiling benchmark...$(RESET)"
	$(GO) test -bench=. -benchtime=5s -cpuprofile=cpu.prof ./tests/bench/...
	@echo "$(GREEN)Run 'go tool pprof cpu.prof' to analyze$(RESET)"

bench-mem:
	@echo "$(YELLOW)[BENCH] Memory profiling benchmark...$(RESET)"
	$(GO) test -bench=. -benchtime=5s -memprofile=mem.prof ./tests/bench/...
	@echo "$(GREEN)Run 'go tool pprof mem.prof' to analyze$(RESET)"

bench-profile:
	@echo "$(YELLOW)[BENCH] Full profiling (CPU + Memory)...$(RESET)"
	$(GO) test -bench=. -benchtime=5s -cpuprofile=cpu.prof -memprofile=mem.prof ./tests/bench/...
	@echo "$(GREEN)CPU profile: go tool pprof cpu.prof$(RESET)"
	@echo "$(GREEN)Mem profile:  go tool pprof mem.prof$(RESET)"

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
	docker run -p 1883:1883 -p 9090:9090 shark-mqtt:latest

docker-test:
	@echo "$(GREEN)[DOCKER] Running smoke test...$(RESET)"
	bash scripts/docker-test.sh

docker-compose-up:
	@echo "$(GREEN)[DOCKER] Starting with docker-compose...$(RESET)"
	docker compose up -d

docker-compose-down:
	@echo "$(GREEN)[DOCKER] Stopping docker-compose...$(RESET)"
	docker compose down

# ─── Kubernetes Targets ────────────────────────────────────

k8s-deploy:
	@echo "$(GREEN)[K8S] Deploying to Kubernetes...$(RESET)"
	kubectl apply -k k8s/base/

k8s-deploy-prod:
	@echo "$(GREEN)[K8S] Deploying production overlay...$(RESET)"
	kubectl apply -k k8s/overlays/production/

k8s-delete:
	@echo "$(GREEN)[K8S] Deleting from Kubernetes...$(RESET)"
	kubectl delete -k k8s/base/

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
	@echo "  test-bench        Run benchmarks (full, 5s x 3)"
	@echo "  test-coverage     Generate coverage report"
	@echo ""
	@echo "Benchmark targets:"
	@echo "  bench             Same as test-bench"
	@echo "  bench-quick       Quick benchmark (1s x 1)"
	@echo "  bench-cpu         CPU profiling"
	@echo "  bench-mem         Memory profiling"
	@echo "  bench-profile     Full profiling (CPU + Memory)"
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
	@echo "  docker-build         Build Docker image"
	@echo "  docker-run           Run Docker container"
	@echo "  docker-test          Run Docker smoke test"
	@echo "  docker-compose-up    Start with docker-compose"
	@echo "  docker-compose-down  Stop docker-compose"
	@echo ""
	@echo "Kubernetes targets:"
	@echo "  k8s-deploy           Deploy to Kubernetes (base)"
	@echo "  k8s-delete           Delete from Kubernetes"
	@echo ""
	@echo "Development targets:"
	@echo "  tidy              Clean up go.mod"
	@echo "  verify            Verify dependencies"
	@echo "  ci                Run full CI pipeline"
	@echo ""
	@echo "Other targets:"
	@echo "  clean             Clean build artifacts"
	@echo "  help              Show this help"
