# Prioritized gaps, bugs, and improvements

Priorities:

- P0: blocks correct health implementation or production safety.
- P1: required for a reliable first production slice.
- P2: useful follow-up that should not delay the core slice.

## P0

| Type | Gap or bug | Impact | Required action |
| --- | --- | --- | --- |
| Gap | Resolver discards service role and health policy | Runtime cannot construct any probe | Replace descriptor service names with structured local services |
| Gap | Descriptor has no site service inventory | Instances cannot show never-seen remote services or expected reporters | Resolve static site health inventory into every descriptor |
| Gap | No runtime probe engine | Authored health policy has no effect | Add cancellable HTTP workers and retry state |
| Gap | NATS client has no health publish/subscribe path | Results cannot leave the process | Add a dedicated ephemeral health adapter |
| Gap | No observation identity, ordering, freshness, or reducer contract | Duplicate observers and message loss produce undefined views | Accept and test the contract in this plan before coding |
| Risk | Current NATS routes have no authentication or TLS | A route peer can inject false health and read site traffic | Secure routes before production or explicitly accept a trusted-network deployment |
| Design trap | The runtime has no site-journal composition (`app.open` and `hasEventStorage` were removed) | Health work scoped to an activation would not run while Passive | Compose monitoring at process lifetime in `app.Run` |
| Design trap | Platform peer failover consumes `/health` | Folding target failure into platform health can cause useless ownership churn | Keep target status out of the platform Unhealthy decision |

## P1

| Type | Gap or bug | Impact | Required action |
| --- | --- | --- | --- |
| Misleading behavior | `/health` always reports `internalServices` Healthy | Operators can mistake a placeholder for real service monitoring | Rename or redefine it as monitor-subsystem health |
| Misleading behavior | `/health/ready` always reports Healthy | It does not express Active/Passive or site-view readiness | Define its intended platform meaning separately; do not use it as target status |
| Gap | Event-fabric health checks only the local broker round trip | A partitioned instance can claim the fabric is healthy | Expose route/view completeness separately |
| Gap | No delivery-loss or backpressure policy | A blocked publisher could delay probes or silently lose state | Use bounded latest-value buffering and counters |
| Gap | HTTP success semantics are undocumented | Services can be classified differently by implementations | Standardize GET, 2xx success, no redirects, no proxy, bounded body handling |
| Validation bug | `path` only checks trimming and leading slash | Invalid escapes, fragments, or ambiguous URLs can reach runtime | Parse and validate request paths in the builder |
| Ambiguity | Probe host is not represented | Loopback and machine-IP bindings behave differently | Decide and document machine IP for the first slice |
| Ambiguity | Failure hysteresis defines only the down threshold | Recovery and startup behavior are undefined | One success recovers; startup remains Unknown until evidence |
| Gap | No freshness rule | A dead observer can remain Healthy forever | Derive and enforce `2 * interval + timeout` |
| Gap | No complete API view | Operators and SDK clients cannot consume the result | Add `GET /health/services` on both instance states |
| Validation bug | `task sdk-dotnet` succeeds when no .NET E2E tests are discovered | Generated service-health client behavior could regress while `task all` remains green | Fix test discovery and make zero discovered tests fail before relying on SDK E2E coverage |
| Test gap | Scenario services do not exist | Existing scenarios cannot prove monitoring and will observe expected failures | Add controlled fake services in dedicated scenarios |
| Risk | Primary and Standby schedules can synchronize | Every service receives duplicate bursts | Add bounded observer-specific startup jitter |
| Risk | Unvalidated inbound NATS payloads | Malformed or foreign messages can corrupt or exhaust the view | Bound payload size and validate scope, topology, enum, duration, and version |

## P2

| Type | Gap or improvement | Impact | Recommended follow-up |
| --- | --- | --- | --- |
| Validation limitation | Duplicate service health ports are always rejected | Services behind one listener cannot use different paths | Decide whether shared endpoints are supported |
| Improvement | No request/reply warm-up snapshot | Restart convergence waits up to one probe interval | Add only if measured startup latency is unacceptable |
| Improvement | No configurable recovery threshold | One success immediately recovers | Add only when a real flapping requirement appears |
| Improvement | Only plain HTTP probes exist | TLS, TCP, Windows Service state, or body contracts are unsupported | Add probe types behind the machine interface when required |
| Improvement | No metrics endpoint | Logs and API snapshots are the only diagnostics | Add bounded counters or metrics with controlled service labels |
| Improvement | No service state-change events | There is no durable audit history | Keep out of scope unless a separate audit requirement is accepted |
| Improvement | General distribution abstraction remains a draft | NATS-specific code may later move | Keep narrow interfaces now; do not block health on speculative pluggability |

## Security risk details

The embedded NATS client is in-process and its incidental client listener is
loopback-only. The exposed route listener is different. It binds the machine IP
and currently relies on cluster name matching, without credentials or TLS.

Cluster name is not authentication. Health messages include operational
topology and allow an injector to make a service look Healthy or Unhealthy.
Health status may later drive service failover, which raises the impact.

Recommended production requirement:

- mutual authentication and encryption for route connections;
- credentials or certificates provisioned outside the compiled descriptor;
- no network client listener;
- fixed health subject permissions where NATS authorization supports them; and
- a scenario or integration test that rejects an unauthorized route peer.

If route security is deferred, the rollout document must name the trusted
network assumption and prohibit health status from driving automated service
activation.

## Consistency risk details

No persistence means a disconnected receiver cannot recover messages emitted
during the gap. This is acceptable only because every publication is a complete
snapshot and repeats after every attempt.

Views can still differ:

- before the first report;
- during route partitions;
- for up to one probe interval after recovery;
- near locally measured freshness expiry; and
- when an observer process is down.

The API must expose these conditions. Calling the model "consistent" without
the bounded eventual qualifier would be a correctness bug in documentation and
consumer expectations.

## Operational risk details

The platform must survive every target being down. Otherwise deployment order
becomes circular: the platform cannot start until services are healthy, while
operators need the platform to learn that services are not healthy.

Failures that stop the platform:

- invalid resolved monitor configuration;
- inability to create the local reducer;
- inability to subscribe to its own embedded broker at startup; and
- internal monitor invariant violations.

Failures that do not stop the platform:

- target timeout or connection refusal;
- target non-2xx response;
- missing remote observations;
- site route partition; and
- health publication failure after startup.
