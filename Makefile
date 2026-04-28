.PHONY: build test test-unit test-integration test-race benchmark race clean tidy fmt vet lint \
        docker-build docker-compose-up docker-compose-down docker-test \
        helm-lint k8s-validate k8s-deploy k8s-delete \
        test-coverage all help

GO := go

all: vet build test

# ─── Build ─────────────────────────────────────────────────

build:
	$(GO) build ./...

build-production:
	$(GO) build -ldflags="-s -w" -o bin/shark-mqtt ./cmd/

# ─── Test ──────────────────────────────────────────────────

test: test-unit

test-unit:
	$(GO) test -count=1 ./...

test-integration:
	$(GO) test -v -count=1 -timeout 120s ./tests/integration/...

test-race:
	$(GO) test -race -count=1 ./...

benchmark:
	$(GO) test -bench=. -benchmem -count=3 ./tests/bench/...

test-coverage:
	$(GO) test -coverprofile=coverage.out -covermode=atomic -count=1 ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@$(GO) tool cover -func=coverage.out | grep total

# ─── Code Quality ──────────────────────────────────────────

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

lint:
	golangci-lint run ./...

tidy:
	$(GO) mod tidy

# ─── Docker ────────────────────────────────────────────────

docker-build:
	docker build -f deploy/docker/Dockerfile -t shark-mqtt:latest .

docker-compose-up:
	docker compose -f deploy/docker/docker-compose.yml up -d

docker-compose-down:
	docker compose -f deploy/docker/docker-compose.yml down

docker-test:
	docker compose -f deploy/docker/docker-compose.test.yml up --abort-on-container-exit

# ─── Kubernetes ────────────────────────────────────────────

helm-lint:
	helm lint deploy/k8s/helm/shark-mqtt/

k8s-validate:
	kubectl apply -k deploy/k8s/app/ --dry-run=client

k8s-deploy:
	kubectl apply -k deploy/k8s/app/

k8s-delete:
	kubectl delete -k deploy/k8s/app/

# ─── Clean ─────────────────────────────────────────────────

clean:
	rm -rf bin/ dist/
	rm -f coverage.out coverage.html cpu.prof mem.prof

# ─── Help ──────────────────────────────────────────────────

help:
	@echo "shark-mqtt Makefile"
	@echo ""
	@echo "Build:"
	@echo "  build              Build all packages"
	@echo "  build-production    Build optimized binary"
	@echo ""
	@echo "Test:"
	@echo "  test               Run unit tests"
	@echo "  test-integration   Run integration tests"
	@echo "  test-race          Run tests with race detector"
	@echo "  benchmark          Run benchmarks"
	@echo "  test-coverage      Generate coverage report"
	@echo ""
	@echo "Code Quality:"
	@echo "  vet                Run go vet"
	@echo "  fmt                Format code"
	@echo "  lint               Run golangci-lint"
	@echo "  tidy               Tidy go.mod"
	@echo ""
	@echo "Docker:"
	@echo "  docker-build       Build Docker image"
	@echo "  docker-compose-up  Start with docker-compose"
	@echo "  docker-compose-down Stop docker-compose"
	@echo "  docker-test        Run Docker smoke tests"
	@echo ""
	@echo "Kubernetes:"
	@echo "  helm-lint          Lint Helm chart"
	@echo "  k8s-validate       Dry-run validate K8s manifests"
	@echo "  k8s-deploy         Deploy to Kubernetes"
	@echo "  k8s-delete         Delete from Kubernetes"
	@echo ""
	@echo "Other:"
	@echo "  clean              Clean build artifacts"
	@echo "  help               Show this help"
