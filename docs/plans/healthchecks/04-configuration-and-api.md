# Configuration and API

Status: implemented, including the public API and the generated clients.

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

`GET /health/services` is implemented as a non-domain endpoint. Every Active and
Passive instance serves it, from one `api.Deps` value both surfaces are composed
from. It reads only the instance's in-memory view, needs neither Primary
Ownership nor a durable site projection, and is not in `api.DomainPaths`.

The implemented shape follows the recommendation below with three refinements:

- `distribution` is an object rather than a string. It carries the state plus
  publisher, subscriber, rejection, and drop counters, which is what tells target
  failure from monitor failure from distribution failure.
- `staleObservers` is listed separately from `missingObservers`. An observer that
  stopped talking and one that never started are different faults.
- statuses use the capitalized vocabulary the rest of the API uses, mapped from
  the view's lowercase wire values in one place, and `Machine` and `Role` name
  the instance that produced the snapshot. Two instances of one machine hold
  separate views and may briefly differ, so a response that did not say whose it
  was could not be compared with another.

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

The hardcoded `internalServices: Healthy` label is gone. It is now
`serviceMonitor`, and it reports this instance's own observations about its own
machine having expired — a monitor that stopped producing. It never reports what
a probe found, and a failing one degrades the instance rather than failing it,
because `Unhealthy` is the gate a peer promotes through and the peer's monitor is
no better placed than this one.

Never-reported observers do not count. A process whose monitor could not start
does not serve, so the only thing "never" can mean on a serving instance is "not
yet", during the first interval after startup.

Whether expected site observations are arriving is exposed separately, as
`distribution.state` on the service endpoint. It asks only about observers on
other machines: this instance's own reports reach its view directly and would
report a working site through a broker that had stopped carrying anything, which
is the failure the state exists for. The `eventFabric` check cannot see it — it
round-trips through this instance's own embedded broker.

Any public type change requires:

- `platform/api` source changes;
- HTTP boundary tests;
- regenerated `api-specifications/openapi.yaml`;
- regenerated `api-specifications/openapi.md`;
- regenerated .NET SDK;
- conformance tests; and
- .NET end-to-end coverage.

