# Stage 00: Findings

Analysis of the existing codebase before any change. This stage proposes nothing.

## Scope

The feature is leadership **among client services running on the platform**: one
machine per `unit_type` may host the Active service at a time. The platform is
the source of truth for that state and publishes it to the local client services.

Out of scope, and unchanged by this plan:

- The platform's own primary and standby processes.
- The machine fence (`internal/redundancy.Fence`) and its lock file.
- The process status files and the deployment handover procedure that reads them.

Those mechanisms arbitrate which *platform process* on a machine is active. They
are a separate concern from which *machine hosts the Active client service*, and
they stay as they are. They appear below only where they constrain the new work.

## Terminology

The repository's vocabulary is used throughout.

| Term | Meaning |
| --- | --- |
| platform, platform instance, node | the platform process on one machine, one binary per machine |
| unit | a client service, identified by `unit_type` + `unit_id` |
| `unit_type` | `uint8`, 0 through 255 (`platform/api/api.go:30`) |
| site | the deployment scope with one journal, from the descriptor |
| journal | the site's ordered, retained event history |
| machine fence | the platform's own primary/standby lock, out of scope |

## 1. Current active/passive determination for client services

There is none.

The only active/passive mechanism in the repository is the machine fence, and it
decides which *platform process* runs active capabilities on one machine
(`platform/internal/app/runtime.go:61`). It says nothing about units and cannot:
it is a local file lock scoped to `<project>-<environment>-<site>-<machine>`
(`redundancy/fence.go:17`), so every machine in a site holds its own
independently.

No component anywhere tracks, decides, or reports which machine hosts the Active
instance of a `unit_type`.

## 2. Existing leadership, election, ownership, role components

| Component | Scope | Decides | Relevant here |
| --- | --- | --- | --- |
| `api.RoleMaster` / `api.RoleSlave` | one registration record | nothing, see below | yes, it is the field that looks like this feature |
| `internal/registration` | site | which proposal claims a `unit_type`+`unit_id` key | yes, as the model to follow |
| `redundancy.Fence`, `State`, `ProcessRole`, `Status` | one machine, platform processes | platform process activity | no, out of scope |
| NATS JetStream Raft | journal replica group | which replica leads the stream | no, fully internal |

### `role: Master | Slave` is decorative

`platform/api/api.go:36` declares `Role *string` on `RegistrationRequest` and
`api.go:64` echoes it on `Registration`. It is optional, client-supplied, and
validated only for membership in `{Master, Slave}`.

Nothing enforces it. Two units of the same `unit_type` on different machines may
both register as `Master` and both are accepted, because the registration key is
`unit_type`+`unit_id` and a distinct `unit_id` is a distinct key with no conflict.
The platform never assigns it, never revokes it, and never notifies anyone when it
changes. It is a label a client asserts about itself and the platform stores.

This is the closest thing in the codebase to the requested model and it carries
none of its semantics. It should not be extended in place: making a client-supplied
field authoritative would mean the platform sometimes overrides what the client
sent, in a field the client still owns on write. Stage 01 introduces a separate,
platform-owned concept and leaves this field as the advertisement it is.

### The registration domain is the right model and the wrong rule

Registration is event-sourced onto the site journal, every node folds the same
order, every node derives the same winner, and there is no reconciliation pass
(`docs/02-registration.md:102`). That mechanism is exactly what leadership needs.

Its decision rule is not. Acceptance requires unanimous confirmation from every
expected machine in the descriptor (`docs/02-registration.md:69`), and an offline
expected machine keeps a proposal pending indefinitely with no timeout, expiry, or
forced acceptance (`docs/02-registration.md:71`). Leadership must be granted
precisely when a machine is gone, which is the case registration refuses to
decide.

Leadership therefore reuses registration's mechanics and needs its own rule. This
is the central design problem and is addressed in stage 01.

## 3. Filesystem lock, marker, polling, mutex, shared-state inventory

Complete inventory, located by reading the code.

| # | Mechanism | Location | Purpose | In scope |
| --- | --- | --- | --- | --- |
| 1 | `active.lock` OS file lock | `redundancy/fence.go:14`, `utils/filelock` | platform process activity | no, kept |
| 2 | Lock poll loop, 100 ms | `utils/filelock/lock.go:12` | standby waits for release | no, kept |
| 3 | `process-{primary,standby}.status` | `redundancy/fence.go:31`, `status.go` | platform process evidence | no, kept |
| 4 | Atomic status write | `status.go:48`, `utils/atomicfile` | prevents torn reads of item 3 | no, kept |
| 5 | Deployment handover gating | `docs/operations/deployment.md:131` | tooling polls item 3 | no, kept |
| 6 | Test and scenario observation | `app_test.go:550`, `scenarios/warm_standby_test.go` | waits on item 3 | no, kept |
| 7 | E2E control-directory marker | `sdk-dotnet/README.md:45`, `RegistrationTests.cs` | test harness handshake | no, test-only |
| 8 | JetStream file storage | configured storage directory | durable journal | no, storage not coordination |

There are no marker files, no shared-filesystem coordination between machines, and
no named or unnamed OS mutexes anywhere in the repository. Item 1 is the only lock.

**The conclusion matters for the target direction.** The brief asked for
filesystem-based coordination to be replaced by a local API plus Named Pipe
events. Within this feature's scope there is nothing to replace: no filesystem
mechanism participates in client-service leadership, because client-service
leadership does not exist yet. Every item above belongs to platform process
redundancy, which is explicitly retained.

This is a greenfield addition, not a migration. No stage in this plan removes a
filesystem mechanism, and none should be invented in order to have one to remove.

## 4. Existing IPC mechanisms

| Mechanism | Direction | Transport | Notes |
| --- | --- | --- | --- |
| Public HTTP API | unit to platform, request/response | TCP | 4 operations, all registration |
| Event Fabric | machine to machine | NATS | inter-node coordination, out of scope per brief |
| Status files | platform to tooling | filesystem | platform processes only |
| Operational events | platform to operator | stderr, optional JSONL | independent of NATS by design |

**There is no named pipe, unix socket, shared memory, or any other local IPC in
the repository.** There is also no push or notification channel of any kind: the
SDK README states it plainly, "Polling the origin is the only confirmation
mechanism; nothing is pushed" (`sdk-dotnet/README.md:128`).

The notification channel this feature needs has no existing implementation to
extend and no precedent in the codebase to follow.

## 5. .NET SDK to platform communication

The path a client service actually uses today, end to end.

### Transport and shape

```
Opdl.Sdk.E2E / client service
  -> PlatformClient (generated)
  -> Microsoft.Kiota.Bundle 2.0.0, HttpClientRequestAdapter
  -> HTTP/1.1, JSON, over TCP
  -> platform config `address`
  -> internal/httpapi mux -> huma -> platform/api operations
```

- `sdk-dotnet/src/Opdl.Sdk/Opdl.Sdk.csproj:19` is the whole dependency set: one
  package reference, `Microsoft.Kiota.Bundle`.
- Authentication is `AnonymousAuthenticationProvider`
  (`sdk-dotnet/README.md:104`). There is none.
- Four operations exist and that is all of them (`sdk-dotnet/README.md:94`):
  `POST /registrations`, `GET /registrations`, `GET /registrations/conflicts`,
  `GET /registrations/{proposal_id}`.
- Every call takes a `CancellationToken`.
- Errors surface as the generated `Error` exception carrying the platform's
  machine-readable code and HTTP status.

### The client is generated, and regeneration wipes the directory

`conformance-tests/api-specifications/dotnet.go:36` runs `kiota generate` with
`--clean-output` (`dotnet.go:42`) against
`sdk-dotnet/src/Opdl.Sdk/Client` (`dotnet.go:16`).

`--clean-output` deletes the output directory before writing. **Any hand-written
file placed under `Client/` is destroyed on the next `task all`.** The generation
is not a staleness check; it always regenerates and the diff is the signal
(`sdk-dotnet/README.md:61`).

This is the hardest constraint on the SDK side of this feature. A Named Pipe
client cannot be generated from OpenAPI, because OpenAPI describes HTTP. It must
be hand-written, and it must therefore live outside `Client/`, in a sibling
namespace under `Opdl.Sdk` that the generator never touches. Stage 04 depends on
this.

### Base URL discovery does not exist

`BaseUrl` is set by the caller on the request adapter and is not part of the
contract, because "the platform listens on a deployment-specific address"
(`sdk-dotnet/README.md:90`). The E2E tests receive it through the
`OPDL_PLATFORM_BASEURL_A` and `_B` environment variables, supplied by the Go
harness.

So a real client service has no supported way to find its local platform. It is
configured out of band. For a feature whose entire value is that a unit asks its
*local* platform about its role, this gap has to be closed. A Named Pipe helps
here: a pipe name derived from deployment identity is discoverable in a way a TCP
port is not.

### The listener belongs to the fence holder

The HTTP listener is owned by the active platform process; a standby binds
nothing (`docs/01-architecture.md:256`). Both processes bind the same configured
address, and the promoted process rebinds it, so from a unit's perspective there
is a stable local endpoint with a short gap during promotion. The measured
promotion window is roughly 130 to 190 ms on the development baseline
(`docs/01-architecture.md:367`).

This is a reconnect concern, not a blocker. It does mean the Named Pipe server in
stage 03 has the same lifetime as the HTTP listener, and that units must treat
disconnection as routine rather than exceptional.

### Contract divergences noticed while reading

Not part of this feature. Recorded because they were found and will confuse
whoever implements stage 02.

- `sdk-dotnet/README.md:134` shows `client.Registrations[7][42].Status.GetAsync()`,
  indexed by unit type and unit ID. The contract's status route is
  `/registrations/{proposal_id}` (`platform/api/http.go:125`) and the generated
  builder is `WithProposal_ItemRequestBuilder`. The README example does not match
  the generated client.
- `sdk-dotnet/README.md:180` shows catching a `409` for
  `registration_key_conflict`. `docs/02-registration.md:39` states there is no
  immediate 409 and that a conflict is a projected outcome.

Both are stale README examples rather than code defects. They belong in
`docs/bugs/` or a small doc fix, not in this plan.

## Consequences carried into the plan

1. Client-service leadership must be built from nothing. No existing mechanism,
   including the machine fence, can be extended to provide it.
2. It belongs on the site journal, following the registration package's
   event-sourced shape, with a decision rule that tolerates a lost machine.
3. `role: Master | Slave` stays an advertisement. Platform-assigned leadership is
   a separate, platform-owned field.
4. The local API gains leadership queries. That is a normal contract change and
   flows to the SDK through the existing generation pipeline.
5. The Named Pipe client must live outside `Client/` or `--clean-output` deletes
   it.
6. Base URL and pipe name discovery must be defined. Today there is none.
7. Nothing filesystem-based is being replaced. This is an addition.
