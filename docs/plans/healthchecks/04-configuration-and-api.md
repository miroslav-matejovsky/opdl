# Configuration and API

## Blueprint

The existing service block is sufficient for the first HTTP implementation:

```hcl
service "alarm-service" {
  role = "master"

  health_check {
    type     = "http"
    port     = 9102
    path     = "/health"
    interval = "5s"
    timeout  = "1s"
    retries  = 2
  }
}
```

Do not add duplicate site inventory or observer lists to HCL. The builder can
derive them from site machines, service blocks, Primary presence, and Standby
presence.

## Blueprint validation improvements

Before resolution, tighten the HTTP path contract:

- parse it as an HTTP request path;
- require one leading slash;
- reject schemes, hosts, fragments, control characters, and invalid escapes;
- decide explicitly whether query strings are supported; and
- keep authored casing and path bytes unchanged after validation.

Recommendation: allow a path and optional query in the existing `path` field,
but reject fragments and absolute URLs. Construct the request with `net/url`
rather than string concatenation.

Revisit the blanket port-collision rule. A health endpoint is a target, not a
listener owned by the platform. Two service identities may intentionally share
one HTTP listener and use different paths. Keep collision checks against known
platform listeners when the target binds the same machine IP, but make
service-to-service duplicate port rejection an explicit product decision.

## Deployment descriptor

Replace the lossy `Services []string` contract with structured local service
definitions in both:

- `builder/deployment`
- `platform/config`

Recommended local structure:

```text
Service {
    Name
    Role
    HealthCheck {
        Type
        Port
        Path
        Interval
        Timeout
        Retries
    }
}
```

Add a separate static site health inventory:

```text
SiteService {
    Machine
    MachineProfile
    Service
    ServiceRole
    ObserverRoles
    FreshFor
}
```

`ObserverRoles` is `primary` plus `standby` when that target machine deploys a
Standby. `FreshFor` is resolved from the target service's probe policy so every
receiver validates the same lifetime without carrying remote probe endpoints.

Do not copy remote health-check ports and paths into every machine descriptor.
Only local instances need local probe details. Every instance needs remote
identity, expected observers, and freshness policy to build a complete view.

Descriptor validation must prove:

- local service names are unique and nonempty;
- roles and probe types are known;
- durations and retry counts remain valid after JSON decoding;
- every local service has exactly one matching site inventory entry;
- every inventory machine belongs to the descriptor site;
- inventory keys are unique;
- observer roles match whether the target machine has a Standby;
- `fresh_for` matches or safely bounds the derived policy; and
- all required structured fields are present, not silently zero-valued.

Update resolver tests, deployment tests, platform config tests, embedded
descriptor fixtures, and descriptor conformance signatures together. This
repository intentionally keeps independent builder and runtime contract types.

## Runtime target construction

`internal/app` should translate each local descriptor service into a machine
package target:

```text
http://<descriptor machine IP>:<service port><service path>
```

The machine package receives typed durations and a prevalidated URL. It does not
parse deployment strings or import `platform/config`.

The app adapter also supplies immutable observer identity:

- project;
- environment;
- site;
- machine and profile;
- service and service role;
- fixed platform instance role;
- process-start epoch; and
- derived freshness duration.

## Public API

Add a non-domain endpoint:

```text
GET /health/services
```

Every Active and Passive instance serves it. It reads only the instance's
in-memory view and does not require Primary Ownership or a durable site
projection. It must not be added to `api.DomainPaths`.

Recommended response shape:

```json
{
  "site": "north",
  "generatedAtUtc": "2026-07-28T12:00:00Z",
  "distribution": "Connected",
  "summary": {
    "healthy": 2,
    "degraded": 0,
    "unhealthy": 1,
    "unknown": 1
  },
  "services": [
    {
      "machine": "local-server",
      "machineProfile": "local-server",
      "service": "alarm-service",
      "serviceRole": "master",
      "status": "Healthy",
      "expectedObservers": ["primary", "standby"],
      "missingObservers": [],
      "observations": [
        {
          "observerRole": "primary",
          "status": "Healthy",
          "checkedAtUtc": "2026-07-28T11:59:58Z",
          "receivedAtUtc": "2026-07-28T11:59:58Z",
          "latencyMs": 4,
          "consecutiveFailures": 0,
          "stale": false
        }
      ]
    }
  ]
}
```

The concrete schema should use arrays, not maps, and sort services and
observations deterministically. This makes generated SDK types usable and
responses stable in tests.

Return HTTP 200 when the view contains Unhealthy, Degraded, or Unknown services.
Those are data, not failure to serve the query. Use a standard API error only
when the view itself cannot be read.

## Existing platform health endpoints

Do not make target service state change platform `HealthResponse.Status`,
`/health/ready`, or the peer promotion gate.

The current hardcoded `internalServices: Healthy` label is misleading once a
real service monitor exists. Recommended change:

- rename it to a precise platform subsystem check such as
  `serviceHealthMonitor`; and
- report whether probe scheduling and the in-memory reducer are running, not
  whether every target service is Healthy.

Distribution connectivity can remain a platform degradation. The existing
`eventFabric` check proves only the local broker round trip, so the service API
must separately expose whether expected site observations are arriving.

Any public type change requires:

- `platform/api` source changes;
- HTTP boundary tests;
- regenerated `api-specifications/openapi.yaml`;
- regenerated `api-specifications/openapi.md`;
- regenerated .NET SDK;
- conformance tests; and
- .NET end-to-end coverage.

