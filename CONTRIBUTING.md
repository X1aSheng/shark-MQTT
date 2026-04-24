# Contributing to Shark-MQTT

Thank you for your interest in contributing to Shark-MQTT! This document provides guidelines and instructions for contributing to the project.

---

## Table of Contents

- [Development Environment](#development-environment)
- [Code Standards](#code-standards)
- [Commit Message Format](#commit-message-format)
- [Pull Request Process](#pull-request-process)
- [Testing Requirements](#testing-requirements)
- [Branch Strategy](#branch-strategy)

---

## Development Environment

### Prerequisites

- **Go 1.22+** - Required for building and running tests
- **Git** - For version control
- **Make** - For running build tasks (optional but recommended)
- **Docker** - For integration tests with Redis

### Setting Up Your Development Environment

1. **Fork and Clone the Repository**
   ```bash
   git clone https://github.com/your-username/shark-mqtt.git
   cd shark-mqtt
   ```

2. **Install Dependencies**
   ```bash
   go mod download
   ```

3. **Run the Tests**
   ```bash
   # Unit tests
   go test -race ./...

   # Integration tests (requires Redis running)
   go test -race -tags=integration ./test/integration/...

   # Or use Make
   make test
   ```

4. **Build the Project**
   ```bash
   go build ./...
   # Or
   make build
   ```

### IDE Configuration

We recommend using **VSCode** with the following extensions:
- Go (by Go Team at Google)
- GitLens

Or **GoLand** with default Go plugin.

---

## Code Standards

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use meaningful variable and function names
- Keep functions focused and small
- Add comments for exported types and functions

### Linting

Before submitting a PR, ensure your code passes all linters:

```bash
make lint
```

This runs:
- `go vet ./...`
- `staticcheck ./...`
- `golangci-lint run`

### Testing

- **All new features must have unit tests**
- **Bug fixes must include regression tests**
- Maintain test coverage ≥ 70% for core modules
- Use table-driven tests where appropriate
- Mock external dependencies in unit tests

---

## Commit Message Format

We follow [Conventional Commits](https://www.conventionalcommits.org/) for commit messages:

```
<type>(<scope>): <subject>

<body>

<footer>
```

### Types

| Type | Description |
|------|-------------|
| `feat` | New feature |
| `fix` | Bug fix |
| `docs` | Documentation changes |
| `style` | Code style changes (formatting, no logic change) |
| `refactor` | Code refactoring |
| `perf` | Performance improvement |
| `test` | Adding or updating tests |
| `chore` | Build process or auxiliary tool changes |

### Examples

```
feat(broker): add support for MQTT 5.0 properties

Implement MQTT 5.0 property encoding/decoding in protocol package.
Supports user properties, content type, and correlation data.

fix(qos): correct retry interval calculation

Retry interval was incorrectly using milliseconds instead of seconds,
causing excessive retry attempts.

docs(readme): update TLS configuration examples

test(session): add persistent session recovery tests

chore(ci): add GitHub Actions workflow
```

---

## Pull Request Process

1. **Create a Feature Branch**
   ```bash
   git checkout -b feature/my-feature
   ```

2. **Make Your Changes**
   - Write clean, well-documented code
   - Add tests for new functionality
   - Update documentation as needed

3. **Run Quality Checks**
   ```bash
   make ci
   ```

4. **Commit Your Changes**
   ```bash
   git add .
   git commit -m "feat(broker): add new feature"
   ```

5. **Push and Create PR**
   ```bash
   git push origin feature/my-feature
   ```
   Then open a pull request on GitHub.

### PR Checklist

Before submitting your PR, ensure:

- [ ] Code follows project style guidelines
- [ ] Tests pass (`make test`)
- [ ] Linting passes (`make lint`)
- [ ] Coverage is maintained (`make test-coverage`)
- [ ] Documentation is updated if needed
- [ ] Commit messages follow Conventional Commits
- [ ] PR description explains the changes

### PR Template

```markdown
## Description
Brief description of changes

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] Documentation update
- [ ] Performance improvement
- [ ] Refactoring

## Testing
- [ ] Unit tests added/updated
- [ ] Integration tests pass
- [ ] Manual testing performed

## Checklist
- [ ] Code follows project style
- [ ] Tests pass
- [ ] Documentation updated
```

---

## Testing Requirements

### Unit Tests

Run unit tests with race detection:
```bash
go test -race ./...
```

### Integration Tests

Integration tests require Redis running locally:
```bash
# Start Redis
docker run -d -p 6379:6379 redis:7-alpine

# Run integration tests
go test -race -tags=integration ./test/integration/...
```

### Benchmark Tests

Run benchmarks to verify performance:
```bash
# Quick benchmark
make bench-quick

# Full benchmark suite
make bench

# With CPU/memory profiling
make bench-cpu
make bench-mem
```

See [docs/performance.md](../docs/performance.md) for detailed profiling workflows.

### Coverage Requirements

- Overall coverage ≥ 60%
- Core modules (broker/, protocol/, store/) ≥ 70%

Generate coverage report:
```bash
make test-coverage
```

---

## Branch Strategy

```
main       - Production-ready code, protected branch
develop    - Integration branch for features
feature/*  - Feature development branches
fix/*      - Bug fix branches
release/*  - Release preparation branches
```

### Workflow

1. Create feature branch from `main`
2. Develop and test your feature
3. Create PR to `main`
4. After review and approval, squash and merge

### Branch Naming

- `feat/description` - New features
- `fix/description` - Bug fixes
- `docs/description` - Documentation
- `refactor/description` - Refactoring
- `test/description` - Test changes

Examples:
- `feat/mqtt5-properties`
- `fix/qos-retry-race`
- `docs/api-reference`

---

## Questions?

If you have questions or need help, please:
- Open an issue on GitHub
- Check existing documentation
- Review closed PRs for similar changes

Thank you for contributing to Shark-MQTT!
