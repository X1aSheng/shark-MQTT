# Security Policy

This document outlines the security policy and best practices for Shark-MQTT.

---

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |
| latest release | :white_check_mark: |
| older releases | :x:                |

---

## Reporting Security Vulnerabilities

If you discover a security vulnerability in Shark-MQTT, please report it responsibly.

**Do NOT** create a public GitHub issue for security vulnerabilities.

Instead, please email security concerns to: [project-security@example.com](mailto:project-security@example.com)

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

## TLS Configuration Best Practices

### Certificate Requirements

Shark-MQTT supports TLS 1.2 and TLS 1.3 for encrypted connections.

**Minimum TLS Version**: TLS 1.2
**Recommended TLS Version**: TLS 1.3

### Certificate Configuration

```go
// Using API with TLS certificates
broker := api.NewBroker(
    api.WithTLSFromFiles("server.crt", "server.key"),
)
```

### Recommended Cipher Suites

For TLS 1.2:
- TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384
- TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
- TLS_RSA_WITH_AES_256_GCM_SHA384 (not recommended for new deployments)

**Disable**:
- SSLv3, TLS 1.0, TLS 1.1 (deprecated and insecure)
- CBC mode ciphers (vulnerable to padding oracle attacks)
- RC4 (broken)
- MD5/SHA1 signatures

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
- TCP 1883: MQTT (consider blocking for public internet)
- TCP 8883: MQTT over TLS (recommended for production)
- TCP 8080: WebSocket/MQTT over WebSocket (if enabled)
- TCP 9090: Prometheus metrics (internal only)

**Recommended Setup**:
```
Internet → Load Balancer (TLS termination) → Shark-MQTT
                                     ↓
                              Internal Network (1883)
```

### Rate Limiting

Implement rate limiting through the plugin system:

```go
// Example rate limiting plugin
pluginManager.Register(plugin.OnPublish, func(ctx *plugin.Context) error {
    // Check rate limit
    if !rateLimiter.Allow(ctx.ClientID) {
        return fmt.Errorf("rate limit exceeded")
    }
    return nil
})
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
