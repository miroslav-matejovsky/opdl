# Backlog: HTTPS probes and certificate sourcing

**Effort**: Medium  
**Value**: Low to Medium (required if monitored services enforce HTTPS endpoints)  

## Context

The platform health monitor currently supports HTTP GET health probes. Probing HTTPS endpoints requires defining a certificate sourcing and trust strategy (such as system root CA trust, custom certificate bundles, or client certificates).

## Proposed Action

- Define the certificate sourcing and trust model for HTTPS health probes.
- Extend health probe configuration to support HTTPS targets with explicit TLS verification options.
- Ensure certificate loading and TLS handshakes perform non-blocking probes and handle expired/invalid certificates cleanly.
