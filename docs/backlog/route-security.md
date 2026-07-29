# Backlog: NATS route security

**Effort**: Medium  
**Value**: High (required for untrusted network deployments)  

## Context

NATS routes across platform instances currently rely on cluster name matching for routing configuration without mutual authentication or TLS encryption. In an untrusted network, an unauthorized NATS peer could inject false health observations or inspect site traffic.

## Proposed Action

- Implement mutually authenticated and TLS-encrypted routes for production NATS clusters.
- Provision private key material and certificates outside compiled descriptors.
- Restrict client listeners to in-process or loopback-only connections unless route security is active.
- For deployments lacking certificate infrastructure, formally constrain deployment to a trusted network.
