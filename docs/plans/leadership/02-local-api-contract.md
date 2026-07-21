# Stage 02: Local API contract

**Effort:** Medium. **Complexity:** Low. **Risk:** Low.

Depends on stage 01. The local API stays the primary request/response mechanism,
as the brief requires. This stage adds the leadership operations to it and lets
them flow to the .NET SDK through the pipeline that already exists.

## Position in the target architecture

The brief is explicit that Named Pipes are for event notification only and that
existing API interactions are not replaced. This stage holds that line:

| Concern | Mechanism |
| --- | --- |
| Query current leadership state | local API, this stage |
| Query which machine holds a unit type | local API, this stage |
| Voluntary release of leadership | local API, this stage |
| Learn that leadership changed | Named Pipe, stage 03 |

The API is authoritative. The pipe is a latency optimization over polling it. A
unit that has just connected, reconnected, or received anything it does not
understand resolves its state by asking the API.

## Operations

Added to the existing contract in `platform/api`, served by `internal/httpapi`
through the same huma registration.

| Operation | Meaning |
| --- | --- |
| `GET /leadership` | Every unit type this site tracks, with holder machine, state, epoch, and lease expiry. |
| `GET /leadership/{unit_type}` | One unit type's leadership: holder machine, whether it is this machine, state, epoch. |
| `POST /leadership/{unit_type}/release` | Voluntary release by the current holder. Supports OR-48, relinquishing leadership on graceful shutdown. |

`GET /leadership/{unit_type}` is the operation a unit calls on startup and after
every reconnect. It is the one that must be correct; the rest are convenience and
tooling.

## Response shape

The answer a unit needs is its own role, not the site's topology. Lead with it:

- `state`: `Active` or `Standby`, from the calling machine's point of view.
- `epoch`: the fencing token from stage 01.
- `holder`: the machine holding the unit type, which may be this machine or
  another, and may be absent when nobody holds it.
- `unit_type`, and the lease expiry for diagnostics.

A unit that only reads `state` and `epoch` is correct. That is the intended
minimum.

## Answering semantics

Answers come from the answering node's local projection, exactly as registration
queries do (`docs/02-registration.md:43`). The node has caught up before it serves
at all, so a local answer is authoritative for the journal order it has seen.

Two consequences worth writing into the operation docs:

- `state` is this machine's role. The same query on another machine of the site
  answers differently, and that is correct rather than an inconsistency.
- A node whose projection has stalled becomes unready and stops serving, so there
  is no path where the API answers from a stale view. This is existing behavior
  (`docs/01-architecture.md:100`) and it is what makes the API trustworthy as the
  authority behind the pipe.

## Contract mechanics

The closed value set problem in `docs/backlog/api-contract.md` applies directly.
`state` here is a closed set and should be declared with an `enum` struct tag from
the start so Kiota emits a C# enum rather than a string. Doing it for the new
fields does not require fixing the existing `status` and `role` fields, and it
avoids adding a second stringly-typed field to the backlog item.

Regeneration is unchanged: `go test ./conformance-tests/cmd` regenerates
`api-specifications/openapi.yaml` and the Kiota client, and runs inside
`task all`. The new operations reach the SDK with no hand-written client code.

## Relationship to `role: Master | Slave`

Untouched. It stays a client advertisement on `RegistrationRequest` and
`Registration`, as stage 00 established.

Platform-assigned leadership is a separate field on a separate resource. The two
must not be merged: one is what a unit says about itself on registration, the
other is what the platform decides about a machine at runtime. If they shared a
field, a write by the client and a decision by the platform would collide on the
same value.

Whether `role` should eventually be deprecated in favor of leadership is a real
question, and deliberately not answered here. It stays until this feature has
proven itself in a deployment.

## Discovery

Stage 00 established that a client service has no supported way to find its local
platform: `BaseUrl` is set by the caller and the E2E tests receive it through
environment variables.

This stage does not solve discovery for HTTP, and should not invent a mechanism
for it. Stage 03 introduces a pipe name derived from deployment identity, which is
discoverable without configuration. The pragmatic path is that a unit is
configured with the local base URL as it is today, and the pipe name is derived.
If that split proves awkward in practice, the local API can later advertise
nothing and the pipe can carry a bootstrap. That is a decision for after stage 04
has been used.

## Work

1. Leadership request and response types in `platform/api`, with `enum` tags on
   closed sets.
2. Operation registration in `platform/api/http.go`.
3. Handler seam in `api.Handlers`, wired in `internal/httpapi` to the leadership
   query service from stage 01, following the existing pattern where `httpapi`
   holds no identity and learns nothing about how the decision is made.
4. Regenerate contract and SDK.
5. Update `api-specifications/openapi.md` and the SDK README usage section.

## Tests

- Each operation answers from the local projection in a two-machine site.
- The same unit type answers `Active` on the holder and `Standby` elsewhere.
- Release by a non-holder is refused with a stable machine-readable code.
- The generated C# surface exposes `state` as an enum.
