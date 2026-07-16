# Platform primary/secondary redundancy plan

## Goal

Run one active primary platform process and one active secondary platform
process on each machine by default. Either process must serve traffic and make
progress while the other process is stopped or replaced. A machine may opt out
and run only its primary process.

This plan covers the platform runtime, builder, deployment descriptor, fabric,
registration behavior, generated API artifacts, .NET SDK, packaging, tests, and
documentation. It does not add shared-memory communication or more than two
platform instances per machine.

## Current state

The repository currently assumes one platform process per machine throughout:

- `builder/internal/blueprint` has an unused project-wide
  `features.redundancy` switch.
- The builder and platform deployment descriptors carry that unused switch.
- A descriptor contains one machine IP. API and fabric ports are fixed or come
  from one global runtime configuration section.
- `internal/fabric.Member` is identified only by machine name and IP.
- Registration uses machine name as the confirmation key and calls each machine
  a platform instance.
- Event node identity and event filenames contain no platform instance name.
- The runtime starts one API, one reconciler, one recorder, and one Olric member.
- The .NET SDK is a generated client for one base URL. Consumers construct the
  request adapter themselves.
- Packages contain one binary but no primary/secondary launch specification or
  rolling-upgrade contract.

The existing `Master` and `Slave` values are service registration roles in
`platform/api`. They are unrelated to this plan and must remain unrelated.

## Recommended target model

Use these terms consistently:

| Term | Meaning |
| --- | --- |
| Machine | The physical or virtual deployment node identified by the existing descriptor machine name and IP. |
| Platform instance | One platform process on a machine. Its name is exactly `primary` or `secondary`. |
| Service role | A hosted service's optional `Master` or `Slave` role. It is not a platform-instance role. |

`primary` and `secondary` are stable identities and endpoint labels. They do
not mean leader/follower or active/passive. Both processes run the API,
reconciler, event recorder, and fabric member.

The authored machine topology has a platform block with explicit instance
endpoints and an optional `secondary_enabled` setting. Omission resolves to
`true`. The resolved embedded descriptor contains the actual one- or
two-instance list and explicit API and fabric endpoints. It does not contain a
redundancy feature flag. Production code defines no endpoint defaults. Port
numbers shown in documentation are recommendations only.

Each process selects one embedded instance identity at startup. Two independent
OS processes are required. Running both instances inside one process would not
survive a process failure and would not permit a rolling binary replacement.

Fabric membership becomes platform-instance-aware. Registration remains
machine-scoped: primary and secondary are redundant executors of one machine's
decision. Either instance can write the machine's idempotent confirmation. This
preserves registration progress while either process is stopped for an upgrade.

Registration facts become durable before they are acknowledged. Each platform
instance owns one local file-backed store and may write only to that store. It
opens the sibling instance's store read-only. An OPDL-owned pull reconciler
copies missing immutable facts from the sibling into the process's own store,
so the two local stores converge without an Olric subscription or relay. A
surviving sibling can also read facts not yet copied when the writer stops.

Olric remains the transient site-wide exchange layer between machines. It is
not used to synchronize the primary and secondary stores and is not the
durability authority for acknowledged registration state.

Local synchronization normally produces two copies, but it is asynchronous. A
new fact may temporarily exist only in the writer's file. The process-loss
guarantee therefore also depends on the surviving process retaining read access
to both files on the available machine filesystem. Package upgrades must
preserve both data directories. Whole-machine loss, disk loss, file corruption,
and simultaneous loss of both local stores remain outside the guarantee.

The .NET SDK should provide a hand-written redundant transport layer under the
generated client. It should distribute first attempts across both endpoints and
retry once on the other endpoint for a precisely bounded set of transient
failures. Generated files remain generated.

## Non-goals

- Service `Master`/`Slave` active/passive behavior.
- A third or arbitrary number of platform processes per machine.
- Cross-machine high availability after loss of a whole machine.
- Shared memory or another optimized local inter-process transport.
- Dynamic membership discovery.
- A general service mesh, proxy, or virtual IP.
- Silent compatibility with descriptors produced before this change. Rolling
  N to N+1 runtime compatibility is still required once the new descriptor
  contract is introduced.

## Stages and estimates

Estimates are implementation person-days and include tests and documentation.
They assume one engineer familiar with Go and .NET, available review, and no
long delay resolving Stage 0 decisions.

| Stage | Scope | Estimate | Complexity | Status | Depends on |
| --- | --- | ---: | --- | --- | --- |
| [0](00-decisions-and-contract.md) | Resolve remaining contracts and terminology | 2 days | Medium | In progress | None |
| [1](01-deployment-and-configuration.md) | Blueprint, descriptor, endpoints, and overrides | 5 days | Medium | Complete | Stage 0 decisions required by Stage 1 |
| [2](02-instance-aware-fabric.md) | Instance-aware fabric membership and endpoints | 4 days | Medium | Not started | Stage 1 |
| [3](03-durable-local-storage.md) | File-backed stores and direct sibling synchronization | 9 days | High | Not started | Stage 1 |
| [4](04-active-active-runtime.md) | Two independently managed active runtime processes | 6 days | High | Blocked on Stage 0 decision 5 | Stages 1-3 |
| [5](05-registration-and-api.md) | Machine-scoped registration over merged durable stores | 9 days | High | Blocked on Stage 0 decisions 1 and 2 | Stages 1-4 |
| [6](06-dotnet-sdk.md) | Consumer-transparent active-active routing and failover | 6 days | Medium | Blocked on Stage 0 decision 3 | Stage 5 |
| [7](07-packaging-and-upgrades.md) | Persistent data layout, launch metadata, and rolling upgrades | 7 days | High | Blocked on Stage 0 decision 5 | Stages 4-6 |
| [8](08-system-validation.md) | Failure, recovery, storage, conformance, and final documentation | 8 days | High | Not started | Stages 1-7 |
|  | Total | 56 days |  |  |  |

The storage decision adds 16 person-days to the original 40-day plan. Splitting
the original Stage 2 into instance-aware fabric and durable storage adds 7 days.
The design adds 1 day in Stage 4, 3 days in Stage 5, 2 days in Stage 7, and 3
days in Stage 8. Overall implementation complexity is high. The main risks are
crash-safe cross-platform file writes, deterministic convergence between two
writers, direct sibling synchronization during mixed-version upgrades, and
clear separation of durable local facts from transient fabric projections.

Stages 2 and 3 are independent after Stage 1. Instance-aware Olric membership
does not require durable storage, and local storage synchronization does not
require instance-aware Olric membership. Stage 4 composes both into the active
runtime, so neither can be skipped for the redundancy feature.

Complexity rates implementation and correctness risk, not elapsed time. Medium
stages extend established repository patterns. High stages add durability,
concurrency, cross-process lifecycle, or mixed-version correctness concerns.

The estimates exclude implementing an OS-specific service manager. If OPDL must
own Windows Service or systemd installation and orchestration, add a separate
stage after the Stage 0 ownership decision.

## Cross-stage invariants

Every stage must preserve these rules:

- There are exactly two allowed instance names: `primary` and `secondary`.
- Primary is always present. Secondary is present unless explicitly disabled on
  that machine.
- Both configured instances are active. There is no election, leadership,
  fencing, or passive standby state.
- Machine identity remains compiled in. Runtime configuration can move sockets
  but cannot add an instance or change machine or instance identity.
- Primary and secondary have distinct API, Olric client, and Olric memberlist
  addresses after descriptor defaults and runtime overrides are composed.
- A service `Master` or `Slave` value never selects platform behavior.
- The fabric abstraction remains a named byte-collection boundary. Instance
  awareness changes membership identity, not its storage API.
- Durable registration storage is behind a registration-owned abstraction.
  The first backend is file-based; a later SQLite or other backend must not
  change registration behavior.
- Each process has write capability only for its selected instance store and
  read capability for its sibling store. Stores have distinct persistent paths.
- Each process directly imports missing sibling facts into its own store. This
  synchronization is idempotent, pull-based, and independent of Olric.
- An HTTP success or an accepted status is not exposed until every registration
  fact needed to reproduce it is durable in the writer's local store.
- Olric exchanges registration facts between machines. It does not synchronize
  the two same-machine stores and fabric membership is not a durability
  guarantee.
- A request retried between the two endpoints of one machine remains the same
  registration proposal.
- Loss or rolling restart of one platform process does not prevent the other
  process from serving, reading acknowledged state, and completing new
  operations while the machine filesystem remains available.
- All public APIs and non-obvious behavior are documented with the code that
  owns them.
- `task all` passes at the end of every implementation stage.

## Delivery rule

Stage 1 is complete. The unresolved items in Stage 0 block the later stages
named there. Each later stage may be
implemented as a small reviewable change, but the feature is not
production-ready until Stage 8 passes.
