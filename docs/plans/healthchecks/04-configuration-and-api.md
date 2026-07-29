# Configuration and API

Status: blueprint, descriptor, validation, and runtime construction are
implemented. The public API and generated clients are the next delivery slice.

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

## Blueprint validation

Resolution now:

- parse it as an HTTP request path;
- require one leading slash;
- reject schemes, hosts, fragments, control characters, and invalid escapes;
- allows an optional query string; and
- keep authored casing and path bytes unchanged after validation.

Validation uses `net/url` and preserves the authored bytes.

Two service identities may share one HTTP listener when their paths differ.
An exact duplicate port and path is rejected because it would make one
endpoint answer for two identities. Collisions with platform listeners remain
invalid.

## Deployment descriptor

The lossy `Services []string` contract has been replaced by structured local
service definitions in both:

- `builder/deployment`
- `platform/config`

Implemented local structure:

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

The descriptor also contains a separate static site health inventory:

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

Descriptor validation proves:

- local service names are unique and nonempty;
- roles and probe types are known;
- durations and retry counts remain valid after JSON decoding;
- every local service has exactly one matching site inventory entry;
- every inventory machine belongs to the descriptor site;
- inventory keys are unique;
- observer roles match whether the target machine has a Standby;
- `fresh_for` matches or safely bounds the derived policy; and
- all required structured fields are present, not silently zero-valued.

Builder, platform, embedded descriptor, and descriptor conformance tests cover
the contract. The package manifest intentionally retains service names only
because installation does not consume probe policy.

## Runtime target construction

`internal/app` translates each local descriptor service into a machine package
target:

```text
http://<descriptor machine IP>:<service port><service path>
```

The machine package receives typed durations and a prevalidated URL. It does not
parse deployment strings or import `platform/config`.

The app adapter supplies immutable observation identity:

- project;
- environment;
- site;
- machine;
- service;
- fixed platform instance role;
- durable instance epoch captured at process startup; and
- a publisher-assigned sequence.

Machine profile, service role, expected observers, and derived freshness stay
in the static view inventory instead of being repeated on the wire.

## Public API

The composed health subsystem deliberately has no otherwise-unused public view
accessor yet. Add the accessor with this endpoint so dead-code validation keeps
the API boundary honest.

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

