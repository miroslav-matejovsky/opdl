# Current state and scope

## Repository path today

The authored blueprint already requires a health check for every service:

- `builder/internal/blueprint/service.go` defines service name, service role,
  HTTP port, path, interval, timeout, and retries.
- Blueprint validation requires a known role, HTTP probe type, valid port,
  absolute-looking path, positive durations, `timeout < interval`, and at least
  one retry.
- Machine validation rejects duplicate service names and listener port
  collisions.

That information stops at the builder boundary. The resolver intentionally
calls `machine.ServiceNames()` and writes `[]string` to the deployment
descriptor. `builder/deployment.Descriptor` and `platform/config.Descriptor`
therefore contain service names only. The service role and complete probe policy
are unavailable to the platform runtime.

## Runtime capabilities

| Capability | Current state |
| --- | --- |
| Authored per-service probe policy | Implemented in blueprint |
| Probe policy in deployment descriptor | Missing |
| Runtime HTTP probe engine | Missing |
| Consecutive failure tracking | Missing |
| In-memory local service status | Missing |
| Site-wide service inventory | Missing |
| NATS publication of health observations | Missing |
| NATS subscription to health observations | Missing |
| In-memory site health view | Missing |
| Service-health HTTP API | Missing |
| Generated SDK service-health API | Missing |
| Multi-instance or multi-machine health scenario | Missing |

## Existing NATS path

Every deployed platform instance starts its own embedded NATS server in
`internal/instance/natsserver`. All Primary and Standby servers at one site form
one cluster. The server:

- accepts route connections on the machine IP;
- accepts platform clients only through an in-process connection;
- starts without waiting for peers;
- retries routes in the background;
- has JetStream disabled; and
- has no route authentication or TLS configuration.

`internal/site/eventfabric.Client` is the only platform NATS client. It can
connect, run a private publish-and-receive health round trip, and close. It has
no general publish or subscribe capability. Its `Check` proves only that this
instance's client and embedded broker can carry a local message. It does not
prove that any expected route or remote instance is connected.

The `eventfabric.Consumer` contract is designed for durable, ordered,
at-least-once site events. It has no implementation and is the wrong contract
for ephemeral service health. Health observations do not need a total site
order, acknowledgement, replay, or persistence.

## Existing platform health API

Every Active and Passive instance serves:

- `GET /health`
- `GET /health/live`
- `GET /health/ready`
- `GET /health/ha`

These endpoints describe the platform instance. They do not describe hosted
services. `GET /health` currently reports `configuration` and
`internalServices` as Healthy without running checks. It degrades when the local
event-fabric round trip fails. `GET /health/ready` always reports Healthy.

Service targets must not be folded into platform Unhealthy status. The machine
redundancy code uses the peer platform's `/health` response as part of promotion.
Moving Primary Ownership does not repair a failed hosted service because both
platform instances observe the same service process on the same machine.

## Runtime lifecycle constraint

The NATS server and client exist for the whole process lifetime, in both Passive
and Active states. Service monitoring needs the same lifetime.

The runtime has no site-journal composition to attach it to: the unimplemented
`app.open` and `hasEventStorage` were removed, so there is no activation-scoped
composition to be tempted by. Tying service health to a future one would still
be wrong, because a Passive instance must continuously perform the checks this
feature requires.

The monitoring subsystem belongs in `app.Run`, after the embedded broker and
health distribution client are ready and before `runProcess` enters ownership
management.

## Topology gap

Each machine descriptor contains only that machine's service names. It does not
contain the other machines or services at the site. NATS route URLs reveal
network peers but do not carry machine identity, service identity, service role,
or whether a peer machine has a Standby.

Dynamic discovery from received observations is not enough for a complete view.
An instance cannot distinguish:

- a remote service that does not exist;
- a known service that has not reported yet;
- a known service whose platform instances are down; and
- a message that was lost before this instance subscribed.

The builder must resolve a static site service inventory into every descriptor.
This follows the repository's current direction toward a static
Site -> Machine -> Instance hierarchy. It is derived from existing blueprint
blocks, not authored a second time.

## Scenario gap

The scenario blueprint authors `core-services` on port 9101 only to satisfy
validation. No process listens there because no scenario currently probes it.
Once monitoring starts, existing scenarios will correctly observe that target
as Unhealthy. That must not fail platform startup, readiness, or ownership
tests.

Dedicated health scenarios need controllable fake HTTP services. They should
bind each simulated machine IP, because the scenario harness can place multiple
logical machines on one Windows host using different loopback IPs.

## Constraints carried into the plan

- Windows only.
- Both deployed platform instances run the checks.
- Checks continue in Active and Passive states.
- Local redundancy is independent of NATS route health and target service
  health.
- No health-result persistence.
- No durable stream or JetStream requirement.
- No service lifecycle control.
- New public behavior requires OpenAPI, SDK, unit, integration, and black-box
  coverage.

