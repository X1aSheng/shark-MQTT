# Security Policy

This document outlines the security policy and best practices for Shark-MQTT.

---

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.0.x   | :white_check_mark: |
| main    | :white_check_mark: |
| older releases | :x:                |

---

## Reporting Security Vulnerabilities

If you discover a security vulnerability in Shark-MQTT, please report it responsibly.

**Do NOT** create a public GitHub issue for security vulnerabilities.

Instead, please email security concerns to: [project-security@example.com](mailto:project-security@example.com)

---

## Password Security

Shark-MQTT supports bcrypt-hashed passwords for production use.

**Recommended (bcrypt):**
```go
auth := broker.NewStaticAuth()
if err := auth.SetHashedPassword("admin", "secure-password"); err != nil {
    log.Fatal(err)
}
```

**FileAuth with bcrypt hashes in YAML:**
```yaml
users:
  - username: admin
    password: "$2a$10$..."  # pre-hashed with HashPassword()
```

Passwords are auto-detected as bcrypt when they start with `$2a$`, `$2b$`, or `$2y$`.
Plaintext passwords via `AddCredentials()` still work for backward compatibility but
are **not recommended** for production.

See [API.md](guides/API#authentication) for usage examples.

Please include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

You can expect:
- Acknowledgment within 48 hours
- Assessment within 7 days
- Resolution timeline communicated

---

## Rate Limiting & Resource Protection

Shark-MQTT provides configurable protections against resource exhaustion:

| Protection | API Option | Default | Effect |
|-----------|-----------|---------|--------|
| Connection rate limit | `WithMaxConnectRate(n)` | unlimited | Rejects connections exceeding N/sec |
| Publish rate limit | `WithMaxPublishRate(n)` | unlimited | Drops publishes exceeding N/sec/client |
| Max client ID length | `WithMaxClientIDLength(n)` | 128 bytes | Rejects overlong client IDs |
| Max topic filters | `WithMaxTopicFiltersPerSubscribe(n)` | 100 | Rejects SUBSCRIBE with too many filters |
| Max retained topics | `WithMaxRetainedTopics(n)` | 10000 | Drops new retained when limit reached |
| Max will delay | `WithMaxWillDelay(d)` | 24h | Caps will delay interval |

**Example:**
```go
b := broker.New(
    broker.WithAuth(AllowAllAuth{}),
    broker.WithMaxConnectRate(100),       // 100 connections/second max
    broker.WithMaxPublishRate(1000),      // 1000 publishes/second/client max
    broker.WithMaxClientIDLength(64),     // Reject client IDs over 64 bytes
)
```

---

## TLS Configuration Best Practices

### Certificate Requirements

Shark-MQTT supports TLS 1.2 and TLS 1.3 for encrypted connections.

**Minimum TLS Version**: TLS 1.2
**Recommended TLS Version**: TLS 1.3

### Certificate Configuration

```go
// Using API with TLS certificates
cfg := config.DefaultConfig()
cfg.ListenAddr = ":18993"
cfg.TLSEnabled = true
cfg.TLSCertFile = "server.crt"
cfg.TLSKeyFile = "server.key"

broker := api.NewBroker(api.WithConfig(cfg))
```

### Enforced Cipher Suites

Shark-MQTT restricts TLS 1.2 cipher suites to AEAD+forward-secrecy only by default:

- `TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256`
- `TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256`
- `TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384`
- `TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384`
- `TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305`
- `TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305`

**Disabled by default** (no configuration needed):
- SSLv3, TLS 1.0, TLS 1.1 (deprecated and insecure)
- CBC mode ciphers (vulnerable to padding oracle attacks)
- RSA key exchange (no forward secrecy)
- RC4, MD5, SHA1-based ciphers

### mTLS (Mutual TLS)

Shark-MQTT supports client certificate verification via mTLS:

```go
cfg.TLSMutual = true
cfg.TLSCACertFile = "/path/to/ca.pem"
```

When enabled, clients must present a certificate signed by the configured CA.
See [Configuration Guide](guides/CONFIGURATION) for YAML/env var usage.

### Certificate Rotation

Plan for regular certificate rotation:
- Production: Every 90 days
- Development: Every 365 days

Use automated certificate management (e.g., Let's Encrypt with certbot).

---

## Authentication Best Practices

### Password Policies

When using file-based or custom authentication:

- Minimum password length: 12 characters
- Enforce complexity: uppercase, lowercase, digits, symbols
- Implement rate limiting for failed authentication attempts
- Use bcrypt/scrypt/Argon2 for password hashing (never plaintext)

### Client ID Validation

Client IDs should:
- Be unique across the broker
- Not contain sensitive information
- Be validated for length and character restrictions

```go
// Example: Custom client ID validation
func validateClientID(id string) error {
    if len(id) < 1 || len(id) > 23 {
        return fmt.Errorf("client ID must be 1-23 characters")
    }
    // Additional validation as needed
    return nil
}
```

### Connection Limits

Set appropriate limits to prevent resource exhaustion:

```yaml
max_connections: 10000
max_packet_size: 262144
keep_alive: 60
```

---

## Network Security

### Firewall Configuration

**Inbound Rules**:
- TCP 18983: MQTT (consider blocking for public internet)
- TCP 18993: MQTT over TLS (recommended for production)
- TCP 8080: WebSocket/MQTT over WebSocket (if enabled)
- TCP 18999: Prometheus metrics and health endpoints (internal only)

**Recommended Setup**:
```
Internet → Load Balancer (TLS termination) → Shark-MQTT
                                     ↓
                              Internal Network (18983)
```

### Rate Limiting

Implement rate limiting through the plugin system:

```go
type RateLimitPlugin struct{}

func (p *RateLimitPlugin) Name() string { return "rate-limiter" }
func (p *RateLimitPlugin) Hooks() []plugin.Hook {
    return []plugin.Hook{plugin.OnMessage}
}
func (p *RateLimitPlugin) Execute(ctx context.Context, hook plugin.Hook, data *plugin.Context) error {
    if hook == plugin.OnMessage {
        if !rateLimiter.Allow(data.ClientID) {
            return fmt.Errorf("rate limit exceeded for client %s", data.ClientID)
        }
    }
    return nil
}

mgr := plugin.NewManager()
mgr.Register(&RateLimitPlugin{})
```

---

## Topic Access Control

### Topic Naming Conventions

Use hierarchical topic structures for access control:

```
company/department/team/resource/action

Examples:
- acme/iot/sensors/temperature/read
- acme/iot/devices/+/status
- acme/admin/+/config/write
```

### Authorization Best Practices

1. **Deny by default**: Start with deny-all policy
2. **Least privilege**: Grant minimum necessary permissions
3. **Topic isolation**: Separate topics for different security domains
4. **Regular audits**: Review ACL configurations periodically

---

## Data Protection

### Message Encryption

MQTT payload encryption (application layer):
- Use AES-256-GCM for sensitive data
- Implement key rotation
- Never hardcode encryption keys

### Retained Messages

Be cautious with retained messages:
- They persist until explicitly cleared
- May contain sensitive data
- Implement retention policies

```go
// Clear retained message
client.Publish("sensitive/topic", []byte{}, 0, true)
```

### Session Persistence

Persistent sessions (CleanSession=false):
- Store session data securely
- Implement session expiration
- Encrypt session storage if sensitive

---

## Known Security Considerations

### DoS Protection

Built-in protections:
- Max connections limit
- Max packet size limit
- Keep-alive timeout
- Write queue size limit

Additional recommendations:
- Deploy behind a reverse proxy with rate limiting
- Use cloud provider DDoS protection
- Monitor connection patterns

### Authentication Bypass

Current authentication methods:
- `noop`: No authentication (development only!)
- `file`: Username/password from file
- `chain`: Multiple authenticators
- `custom`: User-defined logic

**Never use `noop` in production.**

---

## Security Checklist for Production

- [ ] TLS 1.2+ enabled and properly configured
- [ ] Valid certificates from trusted CA
- [ ] Strong authentication mechanism (not noop)
- [ ] Authorization/ACL configured
- [ ] Rate limiting implemented
- [ ] Connection limits configured
- [ ] Monitoring and alerting enabled
- [ ] Log aggregation configured
- [ ] Network segmentation (separate VLAN/VPC)
- [ ] Regular security updates applied
- [ ] Penetration testing performed
- [ ] Security audit completed

---

## Security Updates

Security patches are released as:
- Minor versions for non-breaking security fixes
- Major versions for breaking security changes

Subscribe to GitHub releases for security notifications.

---

## References

- [OWASP MQTT Security](https://owasp.org/www-pdf-archive/OWASP_MQTT_Security.pdf)
- [NIST TLS Guidelines](https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-52r2.pdf)
- [MQTT Security Fundamentals](https://www.hivemq.com/article/mqtt-security-fundamentals/)
