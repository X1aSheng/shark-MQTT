# Shark-MQTT Documentation

> **Shark-MQTT** - A full-featured MQTT 3.1.1 / 5.0 message broker written in Go.

---

## Documentation Structure

```
docs/
+-- README.md                     <- This file
+-- architecture/                 <- System design & architecture
|   +-- ARCHITECTURE.md           - Project architecture overview
|   +-- DEPLOY.md                 - Deployment guide
|   +-- SECURITY.md               - Security policy & best practices
+-- decisions/                    <- Architecture Decision Records
|   +-- README.md                 - (placeholder for ADRs)
+-- guides/                       <- How-to guides & reference
|   +-- API.md                    - API reference & examples
|   +-- CONFIGURATION.md          - Configuration options
|   +-- DEVELOPMENT.md            - Development setup & workflow
|   +-- PERFORMANCE.md            - Performance tuning
|   +-- TESTING.md                - Testing strategy & guides
+-- planning/                     <- Planning & goals
|   +-- README.md                 - (placeholder for planning docs)
+-- reports/                      <- Audits, reviews & benchmarks
    +-- BENCHMARK-*.md            - Benchmark results
    +-- DEPLOYMENT-VALIDATION-*.md- Cloud deployment verification
    +-- MULTI-ROUND-TEST-*.md     - Multi-round test results
    +-- PROJECT-REVIEW-*.md       - Project review reports
    +-- PROTOCOL-AUDIT-*.md       - MQTT protocol compliance audit
    +-- security_fixes.md         - Security fix log
```

---

## Quick Links

| Section | Description |
|---------|-------------|
| [Architecture](architecture/ARCHITECTURE.md) | System design, components & data flow |
| [API Reference](guides/API.md) | Go API docs, broker options, examples |
| [Configuration](guides/CONFIGURATION.md) | All config options (YAML, env, code) |
| [Security](architecture/SECURITY.md) | TLS, auth, bcrypt, rate limiting |
| [Deployment](architecture/DEPLOY.md) | Docker, docker-compose, K8s |
| [Testing](guides/TESTING.md) | Unit, integration, benchmark tests |
| [Protocol Audit](reports/archive/PROTOCOL-AUDIT-260602-215254.md) | MQTT 3.1.1/5.0 compliance |
| [Latest Review](reports/PROJECT-REVIEW-261017-100640.md) | Full-project review: architecture, tests, 23 defects, doc drift |
| [Broker Comparison](reports/CROSS-BROKER-COMPARISON-261017-121500.md) | shark-mqtt vs smart-mqtt / smart-mqtt-4g / mica-mqtt: protocol, throughput, footprint |
| [Previous Review](reports/PROJECT-REVIEW-260806-143527.md) | V7 reference comparison (smart-mqtt / mica-mqtt) |
| [Smart-MQTT Gap Analysis](reports/SMART-MQTT-GAP-ANALYSIS-261012.md) | Unfinished-detail checklist vs Java smart-mqtt (P0/P1/P2) |
| [K8s / Helm Validation](reports/K8S-HELM-VALIDATION-261016.md) | Real-cluster Helm rollout; multi-port IANA_SVC_NAME defect found and fixed |
| [Cross-Host Throughput](benchmarks/2026-10-15-cross-host-throughput.md) | Cross-host saturation ceiling, QoS 1 RTT bound, write-queue sizing |
| [Load Test Plan](benchmarks/LOAD-TEST-PLAN.md) | How to measure: dimensions, environment rules, pass thresholds, ten measurement traps |
| [Aliyun Baseline](benchmarks/2026-10-17-aliyun-2c4g.md) | 2C4G numbers: rate/payload/fanout sweeps, saturation point, per-connection cost |
| [Latest Deployment Validation](reports/archive/DEPLOYMENT-VALIDATION-260806-124906.md) | Cloud server cleanup + docker/k8s validation |

---

## Reference

- **Module:** `github.com/X1aSheng/shark-mqtt`
- **Go Version:** 1.26+
- **Protocols:** MQTT 3.1.1, MQTT 5.0


