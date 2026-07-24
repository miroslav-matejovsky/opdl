Health Model

The platform exposes health information through HTTP health endpoints intended for operations, monitoring, diagnostics, and redundancy decisions.

Recommended Endpoints:

- GET /health
- GET /health/live
- GET /health/ready
- GET /health/ha

Endpoint Responsibilities

/health

Primary operational health endpoint.

This is the endpoint that administrators, monitoring systems, service wrappers, operational dashboards, and the peer instance will typically consume.

Purpose:

- Overall health assessment
- Operational status
- Dependency status
- Readiness determination
- Basic redundancy visibility

The response should contain:

- Instance identifier
- Role (Primary or Standby)
- Runtime State (Active or Passive)
- Health Status
- Version
- Start Time
- Uptime
- Dependency health results
- High-availability summary

Example:

{
  "status": "Healthy",
  "instanceId": "Primary",
  "role": "Primary",
  "runtimeState": "Active",
  "version": "3.4.1",
  "uptime": "14d 07h 23m",
  "checks": {
    "configuration": "Healthy",
    "database": "Healthy",
    "internalServices": "Healthy"
  }
}

/health/live

Process liveness endpoint.

Purpose:

- Determines whether the process is alive
- Used for service recovery and watchdog scenarios

Typical checks:

- Process running
- Main execution loop functioning
- Critical threads responsive

This endpoint should be lightweight and should not perform expensive dependency validation.

/health/ready

Operational readiness endpoint.

Purpose:

- Determines whether the instance is capable of serving work
- Used by redundancy logic when evaluating promotion eligibility

Typical checks:

- Startup completed
- Configuration loaded
- Required dependencies available
- Database connectivity healthy
- Internal services healthy

Readiness means:

"The instance could successfully operate if it became Active."

/health/ha

Redundancy and ownership diagnostics endpoint.

Purpose:

- High-availability visibility
- Failover troubleshooting
- Ownership diagnostics

Typical information:

- Role
- Runtime State
- Lease Status
- Lease Expiration
- Lease Renewal Time
- Ownership Generation
- Fencing Token (if enabled)

Example:

{
  "role": "Primary",
  "runtimeState": "Active",
  "leaseState": "Owned",
  "leaseExpirationUtc": "2026-07-24T10:15:00Z",
  "ownershipGeneration": 42
}

Health Evaluation Levels

A conservative enterprise implementation typically uses three states:

- Healthy
- Degraded
- Unhealthy

Healthy

The instance can safely perform all responsibilities.

Examples:

- All required dependencies available
- Lease operations functioning
- Database responsive

Degraded

The instance still operates but with reduced capability.

Examples:

- Optional subsystem unavailable
- Monitoring subsystem offline
- Non-critical integration unavailable

Degraded should not automatically trigger failover.

Unhealthy

The instance cannot reliably perform Active responsibilities.

Examples:

- Database unreachable
- Critical initialization failure
- Lease subsystem failure
- Internal processing failure

Unhealthy may allow promotion of the peer if ownership conditions are also satisfied.

Health and Ownership Separation

A fundamental architectural rule is:

Ownership determines who is Active.

Health determines whether promotion is allowed.

Health checks must never:

- Grant ownership
- Transfer ownership
- Override ownership
- Maintain ownership

Lease ownership remains the single authoritative source of Primary Ownership.

Promotion Logic

Each instance periodically:

- Validates lease ownership
- Renews its lease when owner
- Checks peer /health endpoint
- Evaluates promotion eligibility

Promotion is permitted only when:

- The current lease is unavailable due to expiration or release
- The peer instance is Unhealthy

If both conditions are true:

- Acquire lease
- Become Active

Recommended Checks

Required:

- Process health
- Configuration health
- Lease subsystem health
- Internal service health
- Storage or database health
- Time synchronization health (if lease expiration depends on timestamps)

Optional:

- Network connectivity
- External integrations
- Monitoring integrations
- License validation
- Backup subsystem

Operational Principle

The /health endpoint answers:

"Can this instance safely operate?"

The lease answers:

"Which instance is allowed to operate?"

The redundancy design must always trust the lease for ownership and use health only as an eligibility signal for failover and failback decisions.