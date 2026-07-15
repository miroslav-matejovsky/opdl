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
site, machine, role, IP, services, features). The platform trusts it as its own
identity: it is what a registration's origin and an event's node are taken from,
so a client cannot claim to be somewhere it is not. Only settings a site may
change without a rebuild live in the platform's JSON configuration file: the
listen address and the events directory.

### Registration

A client asks the platform to register a unit, keyed by unit type and unit ID.
The request is persisted, then confirmed by every expected platform instance,
and only then accepted. An identical repeat request is an idempotent retry;
reusing a key with different immutable fields is a conflict and is refused with
the stored request untouched. State is in memory today, local to one machine.

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
