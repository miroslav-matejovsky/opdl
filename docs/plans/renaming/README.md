# Fixed-Role Primary/Standby: naming and configuration alignment

Align the platform's local high-availability architecture, its vocabulary, and
its configuration with one intention. Staged so each part can be reviewed and
settled on its own.

Stage 04 is the only one that changes runtime behavior. Every other stage is
naming, structure, or documentation, so the existing scenarios are the regression
net throughout.

## The intention

**Each local process has a fixed role. There are two, and they are named Primary
Instance and Standby Instance. Everything else resolves around that.**

The roles are static and intentional. They are decided at build time, carried in
the package, and visible in the service that runs them. They are not assigned at
runtime, not negotiated, and not exchanged. A Standby Instance that takes over
does not become the Primary Instance; it operates in the Active state until
ownership returns.

This is the part operations sees most, in service names, launch arguments, status
files, startup output, and every operational event. It is therefore stage 01, and
every later stage is downstream of it.

The architecture is **Fixed-Role Primary/Standby operating under a Preferred
Primary policy**. It is not a leader-election architecture, a distributed
consensus system, or a peer-to-peer ownership model, and it must not be described
as one.

## Vocabulary

| Category | Terms |
| --- | --- |
| Roles | Primary Instance, Standby Instance |
| States | Active, Standby |
| Ownership | Primary Ownership, Ownership Acquisition, Ownership Validation, Ownership Release, Ownership Transfer |
| Transitions | Failover, Failback |
| Pattern | Fixed-Role Primary/Standby |
| Policy | Preferred Primary |

The Preferred Primary policy: the Primary Instance normally holds Primary
Ownership and operates Active; the Standby Instance operates Standby; the Standby
Instance assumes responsibility only when Primary Ownership becomes unavailable;
after recovery, upgrade, or maintenance, ownership returns to the Primary
Instance.

### Vocabulary to avoid

Leader, follower, election, leadership, consensus, quorum, distributed lock,
leadership token.

## Cross-cutting rule: what the vocabulary rule does not govern

This is the most important thing to fix before any stage is executed, because
without it a later cleanup pass will "correct" text that is already right.

A repository-wide search for the avoided vocabulary returns **no term that
describes the platform's primary/standby architecture.** Every hit is one of:

**NATS and JetStream internals.** `platform/internal/eventfabric/nats/*`,
`builder/deployment/deployment.go:298`, `docs/01-architecture.md:178,200,207`,
`docs/operations/troubleshooting.md:29,64,70`,
`docs/operations/monitoring.md:64,90`, `docs/operations/upgrade.md:88`,
`docs/backlog/event-fabric.md:19`.

These describe the site journal's Raft replica group, which genuinely is a
distributed consensus system with a metadata leader, elections, and quorum.
Renaming them would make the documentation wrong.

**The registration domain.** `docs/02-registration.md:69,92,162,165` uses the
words to state that registration deliberately has no leader and no quorum.
Describing an absence is not adopting the vocabulary.

**The rule governs how the platform's own local high availability is described.
It does not govern a third-party consensus protocol the platform depends on, nor
a statement that some other part of the system does not use one.** Stage 07
writes this down permanently.

## What actually needs changing

The banned-word search finds almost nothing. The real work is vocabulary the
search does not catch, because the codebase invented its own words before this
intention was stated.

| Issue | Scale | Stage |
| --- | --- | --- |
| No Windows Service identity exists anywhere in the package contract | new capability | 01 |
| `manifest.role` means the machine role while `-instance` means the instance role | 1 collision, high operator confusion | 01 |
| `ProcessRole` is the type for what the vocabulary calls an Instance | ~40 references | 01 |
| `standby` is both a role and a state, in the same status file | documentation | 02 |
| `fence` is the codebase's word for Primary Ownership | ~180 code, ~40 doc references | 03 |
| **Failback is neither automatic nor configurable** | behavior gap | 04 |
| `promotion` and `reclamation` are the words for Failover and Failback | ~114 references | 05 |
| Event names are `platform.fence_*` | 4 events plus runbooks | 06 |
| Ownership configuration sits beside `standby`, optional | schema | 07 |
| `docs/drafts/requirements.md` section 6 is "Redundancy and Leader Election" | 1 section | 08 |

### The one gap that is not naming

The Preferred Primary policy says ownership returns to the Primary Instance after
recovery "according to the configured failback policy". Verified against the code
and the scenarios: **it does not, and there is no such policy.**

A returning Primary Instance finds ownership held, runs as a waiter, and sits at
`role=primary, state=standby` indefinitely. Ownership returns only when an
operator or deployment tooling stops the Active Standby. That is deliberate and
documented, but it is a hardcoded manual-only failback policy rather than a
configured one.

Stage 04 covers it, and it must be settled before stage 05, because how failback
is named depends on what failback does.

## Stages

Ordered so the intention lands first and each later stage inherits settled
vocabulary. Stages 02 through 05 are mostly mechanical once 01 is agreed.

| Stage | Subject | Effort | Risk |
| --- | --- | --- | --- |
| [01](01-fixed-roles.md) | Fixed roles and the operator-visible instance identity | Medium | Medium |
| [02](02-states.md) | Active and Standby states, and the role/state collision | Small | Low |
| [03](03-ownership.md) | Primary Ownership vocabulary in code | Medium | Low |
| [04](04-failback-policy.md) | **Failback policy: behavior, not naming** | Large | High |
| [05](05-transitions.md) | Failover and Failback vocabulary | Small | Low |
| [06](06-operational-contract.md) | Event names and runbooks | Small | Medium |
| [07](07-configuration.md) | Ownership configuration under standby, required | Medium | Medium |
| [08](08-scope-and-requirements.md) | Scope boundaries, requirements draft, adjacent plans | Small | Low |

Stage 04 is the only behavior change, and the only Large item. Stage 01 carries
the only other new capability. Stage 07 is the only one that changes a derived
value. Stage 06 is the only one that breaks an existing operational contract.

Stage 04 has a scoping decision (its D2) that can remove the Large, High-risk work
entirely: keep today's behavior, name it `manual`, and defer the automatic
mechanism. Worth answering early, because it determines the size of the plan.

### Dependencies

```text
01 fixed roles          <- the intention; everything else assumes it
   |
   +-- 02 states -------------------+   (one state's meaning depends on 04)
   |                                |
   +-- 03 ownership ----+           |
   |                    |           |
   +-- 04 failback -----+-- 05 transitions
   |   (behavior)       |           |
   |                    +-----------+-- 06 operational contract
   |                                    (events name roles, ownership, transitions)
   |
   +-- 07 configuration (schema names roles, ownership, and the failback policy)

08 scope and requirements  <- independent, but settle its carve-out before any other stage
```

Recommended execution order: settle 08's carve-out rule and 04's D2 scoping
decision, then 01, then 04 and 07 together so the `standby` block is restructured
once, then 03 and 05, then 02, then 06 last so runbooks are rewritten once
against final names and final behavior.

## How to use this

Each stage file states its intent, the audited current state with file and line
references, the target, the decisions that need an answer, the work, and how it
is validated. Decisions are listed separately from work so a stage can be settled
without reading the implementation detail.

Open decisions are collected per stage rather than here, so they can be addressed
one at a time.
