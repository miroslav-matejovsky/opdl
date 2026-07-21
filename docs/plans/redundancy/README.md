# Align platform redundancy with the goal architecture

Bring the running platform in line with [redundancy-goal.md](../redundancy-goal.md)
and with the deployment contract the builder now produces.

This plan replaces an earlier one that was about vocabulary. That work is done and
is not repeated here. What remains is behavior: the builder describes two
independent runtimes, and the platform still runs one runtime with a second
process attached to it.

## Where the gap is

The blueprint and the deployment descriptor already state the goal architecture.
Each instance has its own API port, its own Event Fabric ports, its own Windows
Service identity, and its own resolved NATS topology. A machine contributes one or
two **peers** to its site, and peers are instances.

The runtime does not read any of that. It composes one configuration per machine
from a shared TOML file, binds one API address, and runs one NATS server that the
Standby Instance is deliberately excluded from. The Standby Instance is not an
independent runtime; it is a waiter that borrows the Active instance's endpoints.

So the descriptor is ahead of the runtime, and the two disagree today. That is the
whole subject of this plan.

## What the goal requires that the platform does not do

| Goal | Reference | Today |
| --- | --- | --- |
| Independent network ports | goal:68 | One API address from TOML, one NATS server per machine |
| Independent configuration context | goal:67 | One TOML file per machine, read by both instances |
| Runtime states are Active and Passive | goal:20-22 | States are `active` and `standby`; `standby` is also a role |
| Ownership is transferred back per a configured failback policy | goal:53-58 | No policy exists; failback is operator-only |
| No lock files, marker files, or filesystem coordination | goal:44 | Ownership is a mutex and correct; status files need an explicit ruling |
| Platform-to-service notifications use Named Pipes | goal:75 | No named pipe exists anywhere in the repo |

Two things the goal requires are already true and need no stage: ownership is a
Windows Named Mutex and is the single source of truth, and the roles are fixed,
build-time, and operator-visible.

## Stages

Ordered so the validation gate is restored before anything changes behavior, and
so each stage that touches the runtime lands on settled names.

| Stage | Subject | Effort | Complexity | State |
| --- | --- | --- | --- | --- |
| [01](01-restore-the-gate.md) | Restore the deadcode gate | Small | Low | done |
| [02](02-active-and-passive.md) | Active and Passive runtime states | Small | Low | |
| [03](03-ownership-contract.md) | Finish the ownership rename: blueprint, descriptor, events | Medium | Medium | |
| [04](04-instance-configuration.md) | Independent configuration per instance | Medium | High | |
| [05](05-instance-event-fabric.md) | Independent Event Fabric per instance | Large | High | |
| [06](06-failback-policy.md) | Failback policy | Large | High | |
| [07](07-service-notifications.md) | Platform-to-service notifications over Named Pipes | Large | Medium | |
| [08](08-documentation.md) | Documentation, requirements, and scope | Small | Low | |

**Scenarios are off until the redundancy implementation changes.** `task all`
currently starts no process and binds no socket. Stages 02 and 03 are
compiler-checked renames and lose little; stages 04 onward are not, and each states
the scenario coverage it needs. The suite has to be back before stage 04 lands, and
it needs updating for the two-runtime model as part of stages 04 and 05. See stage
01.

**Effort** is how much work it is. **Complexity** is how much can go wrong that a
compiler will not catch. Stage 04 is Medium effort and High complexity for exactly
that reason: the change is small and the failure mode is a machine that starts and
serves the wrong thing.

### Dependencies

```text
01 restore the gate  done
   |
   +-- 02 active/passive     (operator-visible names, no behavior)
   |
   +-- 03 ownership contract (operator-visible names, no behavior)
   |
   +-- 04 instance configuration  <- the first behavioral stage
          |
          +-- 05 instance event fabric   <- the largest, needs 04's config split
                 |
                 +-- 06 failback policy  <- needs a settled two-runtime model
                        |
                        +-- 07 service notifications

08 documentation  <- last, so the runbooks are rewritten once
```

Stages 01 through 03 can be done in any order and change no behavior. Stage 04 is
the first that can break a deployment.

### The one ordering rule that matters

**Stage 05 must land as a single change.** Starting the Standby Instance's NATS
server without taking its own resolved topology, or taking the topology without
starting the server, both produce an instance pointed at an address nothing is
listening on. That is a defect this codebase has already had once; see stage 05.

## Vocabulary

Set by [redundancy-goal.md](../redundancy-goal.md) and already applied to the
code. Repeated here only as the check to apply when writing new text.

| Category | Terms |
| --- | --- |
| Pattern | Fixed-Role Primary/Standby |
| Policy | Preferred Primary |
| Roles | Primary Instance, Standby Instance |
| States | Active, Passive |
| Ownership | Primary Ownership, Ownership Acquisition, Validation, Release, Transfer |
| Transitions | Failover, Failback |
| Mechanism | Windows Named Mutex |

Avoid: leader, follower, election, leadership, consensus, quorum, slot,
distributed lock, leadership token.

### What the vocabulary rule does not govern

Carry this into every stage, or a cleanup pass will "correct" text that is right.

The banned words appear in two places where they are accurate and must stay:

**NATS and JetStream internals** (`platform/internal/eventfabric/nats/*`,
`docs/01-architecture.md`, the operations runbooks, `docs/backlog/event-fabric.md`).
The site journal really is a Raft group with a metadata leader, elections, and
quorum. Renaming that would make the documentation wrong.

**The registration domain** (`docs/02-registration.md`), which uses the words to
state that registration deliberately has **no** leader and no quorum. Describing an
absence is not adopting the vocabulary.

The rule governs how the platform's own local high availability is described. It
does not govern a third-party consensus protocol the platform depends on, nor a
statement that some other part of the system does not use one. Stage 08 writes this
down permanently.

## Carried findings

These were found while the builder work landed and are not re-derived in the
stages that own them.

| Finding | Owned by |
| --- | --- |
| `task all` runs no scenarios, so nothing proves a package boots | 01, then 04 |
| The runtime reads the Primary Instance's NATS topology whichever instance runs | 05 |
| JetStream replica placement is unconstrained once a machine runs two servers | 05 |
| The API address has two sources of truth: descriptor and TOML | 04 |
| Blind sed on prose compiles and produces plausible nonsense | 02, 03 |
| `TestFourMachineStorageTopologyAndFailure` is flaky under full-suite load | 01 |

## How to use this

Each stage states its intent, the audited current state with file references, the
target, the decisions that need an answer, the work, and how it is validated.
Decisions are listed separately from work so a stage can be settled without reading
the implementation detail.
