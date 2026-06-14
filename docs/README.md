# Shark-MQTT Documentation

> **Shark-MQTT** — A full-featured MQTT 3.1.1 / 5.0 message broker written in Go.

---

## Documentation Structure

```
docs/
├── README.md                     ← This file
├── architecture/                 ← System design & architecture
│   ├── ARCHITECTURE.md           ─ Project architecture overview
│   ├── DEPLOY.md                 ─ Deployment guide
│   └── SECURITY.md               ─ Security policy & best practices
├── decisions/                    ← Architecture Decision Records
│   └── README.md                 ─ (placeholder for ADRs)
├── guides/                       ← How-to guides & reference
│   ├── API.md                    ─ API reference & examples
│   ├── CONFIGURATION.md          ─ Configuration options
│   ├── DEVELOPMENT.md            ─ Development setup & workflow
│   ├── PERFORMANCE.md            ─ Performance tuning
│   └── TESTING.md                ─ Testing strategy & guides
├── planning/                     ← Planning & goals
│   └── README.md                 ─ (placeholder for planning docs)
└── reports/                      ← Audits, reviews & benchmarks
    ├── BENCHMARK-*.md            ─ Benchmark results
    ├── DEPLOYMENT-VALIDATION-*.md─ Cloud deployment verification
    ├── MULTI-ROUND-TEST-*.md     ─ Multi-round test results
    ├── PROJECT-REVIEW-*.md       ─ Project review reports
    ├── PROTOCOL-AUDIT-*.md       ─ MQTT protocol compliance audit
    └── security_fixes.md         ─ Security fix log
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
| [Protocol Audit](reports/PROTOCOL-AUDIT-260602-215254.md) | MQTT 3.1.1/5.0 compliance |
| [Latest Review](reports/PROJECT-REVIEW-260613-221900.md) | Most recent project review |

---

## Reference

- **Module:** `github.com/X1aSheng/shark-mqtt`
- **Go Version:** 1.26+
- **Protocols:** MQTT 3.1.1, MQTT 5.0
