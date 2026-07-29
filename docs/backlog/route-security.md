# Backlog: NATS route security

**Priority**: P0 before untrusted-network production

**Effort**: Medium

**Value**: High

## Context

NATS routes across platform instances currently rely on cluster name matching
for routing configuration without mutual authentication or TLS encryption. In
an untrusted network, an unauthorized NATS peer could inject false health
observations or inspect site traffic.

The in-process client connection is not the exposure. The route listener binds
the machine address so peer brokers can reach it. Health observations and any
future site traffic cross that listener.

## Recommendation

- Implement mutually authenticated and TLS-encrypted routes for production NATS
  clusters.
- Provision private key material and certificates outside compiled descriptors.
- Restrict client listeners to in-process or loopback-only connections unless
  route security is active.
- For deployments lacking certificate infrastructure, formally constrain
  deployment to a trusted network.

Use machine identities issued by the deployment environment. Define certificate
provisioning, trust roots, rotation, expiry behavior, and rollout order before
adding configuration fields.

## Required coverage

- A route with a trusted peer certificate connects.
- An unknown issuer, wrong identity, expired certificate, or missing client
  certificate is rejected.
- Certificate and private-key material never appears in a compiled descriptor,
  event, health payload, or application log.
- Mixed secured and unsecured route configuration fails before the broker
  starts.
- Rotation can be completed without leaving the site permanently partitioned.
- Health route-partition behavior remains visible through
  `distribution.state`.

## Acceptance

- The deployment threat model states whether route peers are trusted.
- Every production route is mutually authenticated and encrypted, or the
  deployment is explicitly limited to a trusted network.
- Security failures are actionable without exposing key material.
- `task all` and dedicated route-security integration tests pass.
