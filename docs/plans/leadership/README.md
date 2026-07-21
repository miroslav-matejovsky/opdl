# Client service leadership plan

A staged plan to give the platform authoritative leadership over the client
services running on it: one machine per `unit_type` hosts the Active service, the
platform decides it, and local units learn about changes without polling.

Nothing here is implemented. Stage 00 is analysis of what exists today.

## Scope

In scope: leadership among **units**, the client services that register with the
platform.

Out of scope and unchanged:

- The platform's own primary and standby processes.
- The machine fence (`internal/redundancy.Fence`) and its lock file.
- Process status files and the deployment handover procedure that reads them.
- Inter-node transport details.

The machine fence decides which *platform process* on a machine is active. This
feature decides which *machine* hosts the Active client service for a unit type.
They are separate concerns and the fence stays as it is.

## The headline findings

**Client service leadership does not exist.** No component tracks, decides, or
reports which machine hosts the Active instance of a unit type. Requirements
OR-36 through OR-39 are unimplemented.

**`role: Master | Slave` is decorative.** `platform/api/api.go:36` is optional,
client-supplied, and validated only for membership in the set. Two units of the
same type on different machines may both register as `Master` and both are
accepted. The platform never assigns it, revokes it, or notifies anyone. It is the
field that looks like this feature and carries none of its semantics.

**There is nothing filesystem-based to replace.** The brief asked for filesystem
coordination to be replaced by a local API plus Named Pipe events. Within this
scope there is no target: the only lock in the repository is the machine fence,
and the status files belong to platform process redundancy. Both are explicitly
retained. This is a greenfield addition, not a migration, and no stage invents a
filesystem mechanism in order to have one to remove.

**There is no push channel of any kind.** No named pipe, socket, or shared memory
exists. The SDK README states it: "Polling the origin is the only confirmation
mechanism; nothing is pushed."

**The SDK's generated client directory is wiped on every build.**
`conformance-tests/api-specifications/dotnet.go:42` passes `--clean-output` to
Kiota against `sdk-dotnet/src/Opdl.Sdk/Client`. Hand-written pipe client code must
live outside that directory or it is deleted silently.

**Registration is the right model and the wrong rule.** Its event sourcing,
journal ordering, and per-node projection are what leadership needs. Its unanimous
acceptance rule, which keeps a proposal pending indefinitely while any expected
machine is offline (`docs/02-registration.md:71`), is the opposite of what
leadership requires: leadership must be granted precisely when a machine is gone.

## Architecture

```text
              site journal (ordered, retained)
                        |
        claimed / granted / renewed / released / revoked
                        |
        +---------------+---------------+
        |                               |
   machine A                       machine B
   platform process                platform process
   (fence holder)                  (fence holder)
        |                               |
   leadership projection           leadership projection
        |                               |
   +----+----+                     +----+----+
   |         |                     |         |
 local     named pipe            local     named pipe
 HTTP API  (events)              HTTP API  (events)
   |         |                     |         |
   +----+----+                     +----+----+
        |                               |
   unit (Active)                   unit (Standby)
```

Leadership is decided on the site journal, following the registration package's
shape. Each platform instance folds the same order and derives the same holder.
Locally, each platform instance is the single source of truth for its own units:
they never see the journal, the fence, or another machine.

### Two mechanisms, one query surface

| Question | Decided by | Scope |
| --- | --- | --- |
| Which platform process on this machine is active? | machine fence, OS file lock | one machine, unchanged |
| Which machine hosts the Active unit of this type? | leadership lease on the journal | the site, new |

A unit asks its local platform and gets one answer. It does not compose the two.

### API and pipe divide the work as the brief requires

| Concern | Mechanism |
| --- | --- |
| Query current state, query holder, release voluntarily | local API |
| Learn that leadership changed | Named Pipe |

The API is authoritative and the pipe is a latency optimization over polling it.
The pipe is deliberately excluded from the safety argument: it cannot deliver to a
process that is paused or gone, which is exactly the case where an old Active must
stop. Safety comes from self-demotion strictly before possible expiry, and from
the epoch as a fencing token. If a later change ever makes correctness depend on
pipe delivery, the design has regressed.

### The epoch

Every grant carries the journal sequence of the granting event. It is monotonic
and never reused. A message with an epoch no greater than the last seen is
discarded. This is what lets the notification channel be lossy, duplicating, and
reordering without threatening correctness, and it is what a unit carries on side
effects that need external ordering.

## Stages

| Stage | Subject | Effort | Complexity | Risk |
| --- | --- | --- | --- | --- |
| [00](00-findings.md) | Findings: current mechanisms, IPC, SDK path | done | n/a | n/a |
| [01](01-leadership-domain.md) | Leadership domain: key, lease, epoch, events | Large | High | High |
| [02](02-local-api-contract.md) | Local API operations and SDK regeneration | Medium | Low | Low |
| [03](03-named-pipe-events.md) | Named Pipe event channel | Medium | High | Medium |
| [04](04-dotnet-sdk-client.md) | .NET SDK leadership client | Medium | Medium | Medium |
| [05](05-validation.md) | Scenarios, observability, documentation | Medium | Low | Low |

Effort: Small is up to about two days, Medium about a week, Large multiple weeks.

Stage 01 is the only one that can be worked before its predecessors are settled,
and it is the one that must be settled before anything else starts. Stages 02 and
03 can proceed in parallel once 01 is decided. Stage 04 needs both.

## What makes this hard

**It introduces the platform's first time-based decision.** Nothing in the current
domain expires. Leases bring a clock assumption the codebase does not currently
carry, and it should be documented and monitored rather than left implicit.

**Self-demotion is a correctness parameter.** A holder that cannot renew must
demote before its lease can expire anywhere else. The margin between the local
renewal deadline and the observers' expiry deadline must exceed worst-case journal
write latency plus clock error, and its invariant should be validated at
configuration load rather than described in prose.

**Disconnection is not staleness.** A unit whose channel is down does not know it
is Active. Reporting `Standby` is the safe default; continuing as Active on a dead
channel is how two Actives happen.

**Platform failover makes every local unit briefly uncertain.** The pipe drops
when the active platform process is replaced, roughly 130 to 190 ms on the
development baseline. That is correct behavior, since during that window the
machine has no process that can speak for it, but it is a real cost and stage 05
measures it rather than assuming it.

## Open questions to settle before stage 01

1. **Candidacy source.** Descriptor or accepted registrations? Recommendation:
   descriptor, because registration's unanimity would make candidacy inherit the
   blocking property this feature exists to avoid. See stage 01.
2. **Grant granularity.** Does a grant cover every `unit_id` of that type on the
   machine? Recommendation: yes, because the grant is to the machine. Per-`unit_id`
   single-active is a different key and should be refused rather than
   half-supported.
3. **Opt-in.** OR-36 says services must be able to declare single-active
   execution. Recommendation: declare it in the descriptor alongside candidacy.
4. **Local API discovery.** A unit has no supported way to find its local
   platform today; `BaseUrl` is set by the caller and the E2E tests get it from
   environment variables. The pipe name can be derived from deployment identity,
   the HTTP base URL cannot. See stage 02.
5. **Windows-only or portable channel.** `utils/filelock` already carries
   per-platform files. Recommendation: a neutral interface with a named-pipe and a
   unix-socket implementation, so Linux CI keeps covering the delivery contract.

## Unrelated defects found during analysis

Recorded because they will confuse whoever implements stage 02. They are stale
README examples, not code defects, and belong in a separate small change.

- `sdk-dotnet/README.md:134` shows `client.Registrations[7][42].Status.GetAsync()`.
  The contract's route is `/registrations/{proposal_id}`.
- `sdk-dotnet/README.md:180` shows catching `409` for
  `registration_key_conflict`. `docs/02-registration.md:39` states there is no
  immediate 409.
- `docs/backlog/redundancy.md:35` references `docs/plan/issues.md`, which does not
  exist.
