# Operational Platform Distribution Line (OPDL)

OPDL builds one deployable platform binary per machine. A project blueprint
describes the machines; the builder stages each machine's deployment descriptor
into the platform binary and packages it. A machine's identity is therefore
compiled in, not configured at the site.

## Architecture

The repository is a Go workspace of four modules, plus a generated .NET SDK.

| Module | Responsibility |
| --- | --- |
| `builder` | Reads a project blueprint (`examples/*/project.hcl`), derives per-machine deployment descriptors, and packages a platform binary per machine. |
| `platform` | The runtime that ships to a machine. Serves the registration HTTP API and records what it does as events. |
| `conformance-tests` | Checks the modules stay compatible: the platform's API contract against `api-specifications/openapi.yaml`, and the builder's descriptor against the platform's. |
| `scenarios` | Black-box, end-to-end checks. Drives the builder and the built binary as external processes, never importing their code. |
| `sdk-dotnet` | The Kiota-generated .NET client for the platform API, with its own end-to-end tests. |

The platform's API contract lives in `platform/api` as Go types and is the
single source the OpenAPI specification and the .NET SDK are generated from.

### Deployment descriptor

Each built binary embeds one `deployment.Descriptor` (project, environment,
site, machine, role, IP, services, features, and its resolved fabric peers).
The platform trusts it as its own identity: it is what a registration's origin,
an event's node, and a fabric member are taken from, so a client cannot claim to
be somewhere it is not. Only settings a site may change without a rebuild live in
the platform's JSON configuration file: the listen address, the events directory,
and fabric adapter overrides.

### Fabric

The fabric (`platform/internal/fabric`) is the platform's distribution boundary:
the one abstraction through which platform code shares state across the machines
of a site. Platform code depends on the `fabric.Fabric` interface; which backend
carries the data is decided by runtime composition alone.

Its only capability in this release is a **named collection**: a map of string
keys to byte values, shared by a site, with create-if-absent, atomic swap, get,
and weakly consistent enumeration. There is no publish, subscribe, or request
yet. Create has one-winner semantics only while membership is stable: during an
Olric join it can briefly report a false win and overwrite the current value.
The package documents exactly what every adapter owes a caller (byte ownership,
stable-membership per-key atomicity, enumeration weakness, membership stability,
behavior after close, context cancellation), and a single contract test suite
runs against every adapter to hold them to its stable-membership guarantees.

| Adapter | Purpose |
| --- | --- |
| `fabric/olric` | Production. An Olric member embedded in the platform process. The only package that imports Olric. |
| `fabric/memory` | In-process, for tests and for holding the production adapter to the same contract. Never composed at runtime. |

**Bootstrap is derived, not discovered.** The builder resolves each machine's
fabric topology from the site portion of the project blueprint, so a machine
boots knowing its site's membership. The Olric adapter derives its addresses from
those topology IPs on fixed ports: `3320` for the client surface and `3322` for
membership, seeding from its peers' IPs on `3322`. That is why a project may not
give two machines the same IP. The `fabric.olric` section of the configuration
file overrides those addresses for development hosts and for scenarios; overrides
move sockets only and never change which machine a process is.

Expected membership always comes from the descriptor, so a member that is
currently unreachable still belongs to the site; `State` reports reachability
separately (`connected`, `degraded`, `disconnected`). A single-machine site is a
one-member fabric and is connected on its own. A site boots in any order: a
machine whose peers are not up yet starts alone and merges when they arrive.

**Startup order.** The event sink opens first, then the fabric, then registration
opens its collections and its reconciler runs one pass, and only then the public
API. A machine that cannot start its fabric, or cannot reconcile, never serves
traffic: a restarted machine owes the site's outstanding requests its answer
before it answers anyone's questions about them. Shutdown reverses it: HTTP
intake stops and drains, reconciliation stops and its pass in flight finishes,
the fabric closes and records `platform.fabric.stopped`, and the event sink
closes last. Nothing is left reading a fabric that is going away.

**No redundancy.** One fabric member per machine, with no primary/secondary
instance, election, or fencing. Olric may partition or replicate internally;
that is a backend detail and not a platform guarantee, and it must not be read as
service redundancy.

The v1 `distdata` package is deprecated and deliberately not ported: Olric is an
adapter behind the fabric here, not an API anything else depends on.

### Registration

> **Implementation staging.** The contender and conflict-query contract below is
> defined in Stage 3. Stages 4 and 5 move storage and the HTTP API to that model;
> until then, the current runtime still has the join limitation described in
> [docs/backlog/fabric.md](docs/backlog/fabric.md).

A client asks the platform to register a unit, keyed by unit type and unit ID.
`POST /registrations` takes the request and answers `202`: the request is a
proposal, and `202` does not mean the unit is registered. The client then polls
`GET /registrations/{unit_type}/{unit_id}/status` on the machine it asked, which
is its only confirmation mechanism. There is no request id: the unit key locates
the request, and nothing client-visible is generated.

**The acceptance boundary is every expected platform instance.** A request is
accepted only once every machine in the site's static deployment topology,
including the origin, has recorded acceptance of that exact proposal. It is not a
quorum, and it is not current live membership: an expected machine that is down
keeps the request pending, indefinitely, rather than being dropped from the vote.
There is no timeout, expiry, or forced acceptance. This is the point of the
design, not a limitation of it.

**Each machine runs a reconciler.** It scans the site's proposals once at startup
and periodically after (`registration.reconcile_interval`, default 1s), validates
them, and records its own confirmation. Nothing is delivered to it and no
instance coordinates the others: every decision is derived from the site's state,
so a pass is a correction rather than a step.

**Contenders converge to one registration.** Every distinct proposal for a unit
key is retained. An already accepted proposal is the incumbent and stays the
winner. If competing proposals race before either is accepted, the earliest
platform-observed request time wins; equal times use the proposal fingerprint as
a deterministic tie-break. Platform clocks are not coordinated, so this is a
best-effort first-writer rule, not a linearizable global ordering. Once all
contenders are visible and membership is stable, the winning proposal is the
registration and every loser is `rejected` with reason
`registration_key_conflict`. A different proposal may be visible or briefly
accepted before that correction completes.

**Conflicts.** An identical repeat request remains an idempotent retry. A
different proposal that is already visible is refused with `409`. A proposal that
wins falsely during a join is retained rather than erased, then rejected by the
reconciler if it loses selection. The unit key is site-local and never scoped by
machine. Without caller identity, a byte-for-byte identical second unit on one
machine is indistinguishable from a retry.

**State lives on the fabric.** The registration package retains proposals,
confirmations, acceptance evidence, and repairable current views, so a false
create cannot erase a contender. `GET /registrations` will list every retained
proposal, including rejected losers. `GET /registrations/conflicts` will group a
key's contenders and identify its winner and losers. The latter is a domain query,
not a health endpoint: a resolved conflict does not make a process unavailable.
Stages 4 and 5 implement these storage and query changes. Notifications,
acknowledgement, retention, and removal remain later work.

State is in memory and is not replayed after a full-site shutdown. Different
sites have separate fabrics, and cross-site uniqueness is not enforced.

> **Fabric limitation.** Olric's join behavior is the reason registration retains
> contenders and reconciles them. See [docs/backlog/fabric.md](docs/backlog/fabric.md)
> for the measured behavior and mitigation rationale.

#### End to end: uncontested proposal

A client registering a unit against a two-machine site, with node B still
starting:

| # | Where | What happens |
| --- | --- | --- |
| 1 | client → node A | `POST /registrations` with the unit key and advertised name. |
| 2 | node A | Retains the proposal under its fingerprint, stamps its own descriptor identity as the origin, creates a repairable current view, states `requested`, and answers **202**. Nothing is registered yet. |
| 3 | node A | Its reconciler validates the proposal and records its own confirmation, stating `confirmed`. One of two expected instances have accepted. |
| 4 | client → node A | `GET /registrations/{unit_type}/{unit_id}/status` → **pending**, with `platform_instances` showing node A accepted and node B pending. The client polls; this is its only confirmation mechanism. |
| 5 | node B | Starts, joins the fabric, and finds the request by scanning: nothing was delivered to it. Its reconciler validates the same record, reaches the same verdict, and records its confirmation. |
| 6 | node A | Sees every expected instance has accepted, records acceptance evidence, and projects the uncontested proposal as the registration. It states `accepted`. |
| 7 | client → node A | Status → **accepted**. `GET /registrations` on either machine now lists it. |

Node B never becomes the origin, and never answers node A's status endpoint. If
node B had never started, step 4 would simply remain the answer, indefinitely.
If a competing proposal appears while membership changes, the reconciler retains
both, selects the incumbent or best-effort first contender, and rejects the
loser instead of treating one Create result as final proof of uniqueness.

### Domain events

Events are how the platform states what it did, and they are a first-class
concept here rather than a logging detail. A platform event is a domain event:
a fact that has already happened, named in the past tense
(`platform.registration.accepted`), recorded at the state transition that owns
it. An event therefore exists if and only if the fact does, which is what makes
events usable as evidence: an exact retry and a rejected input record nothing.
Payloads are a published contract, read from outside the process.

`platform/internal/events` owns only the mechanism: the envelope (id, dotted
type, process-local sequence, UTC timestamp, source subsystem, optional tags),
the recorder, and the sink contract. It declares no events of its own. Each
package declares the events it owns in its own `events.go`, with that package's
full list documented at the top:

| Package | Events |
| --- | --- |
| `platform/internal/registration` | `requested`, `confirmed`, `accepted`, `rejected`, `conflict` |
| `platform/internal/fabric` | `started`, `stopped` |

Recording is synchronous and each record is flushed before the emitting
operation returns, so a scenario that has read an HTTP response can already read
the events that response produced. Setting `events_dir` in the platform's
configuration file enables recording; leaving it empty disables it.

Storage today is JSONL, one file per machine, for development and scenarios
only. The deployment node an event is about is constant for a process run, so
the file is named for it (`events-<project>-<environment>-<site>-<machine>-<role>.jsonl`)
instead of repeating those five fields on every line. A production backend that
pools several machines into one stream would carry the node per record; that is
a property of the sink, not of the event model. See
`platform/internal/events/doc.go` for the concept and the current limitations.

## Working in this repository

- `task all` runs everything and must pass before work is considered complete.
- `task fast` runs checks and unit tests only.
- `task build -- customer-a` builds a project's deployment packages.
- `task --list` shows the rest.

Conventions for humans and agents are in `AGENTS.md`. Staged plans live in
`docs/plan/`, lower-priority follow-ups in `docs/backlog/`, and known unfinished
work in `.todo`.
