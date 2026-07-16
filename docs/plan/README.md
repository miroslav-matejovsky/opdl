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

| Stage | Scope | Estimate | Status | Depends on |
| --- | --- | ---: | --- | --- |
| [0](00-decisions-and-contract.md) | Resolve remaining contracts and terminology | 2 days | In progress | None |
| [1](01-deployment-and-configuration.md) | Blueprint, descriptor, endpoints, and overrides | 5 days | Complete | Stage 0 decisions 1-5 resolved |
| [2](02-instance-aware-fabric.md) | Instance-aware fabric membership and single-process-loss data safety | 6 days | Blocked on Stage 0 decision 4 | Stage 1 |
| [3](03-active-active-runtime.md) | Two independently managed active runtime processes | 5 days | Blocked on Stage 0 decision 5 | Stages 1-2 |
| [4](04-registration-and-api.md) | Machine-scoped registration over redundant executors | 6 days | Blocked on Stage 0 decisions | Stages 1-3 |
| [5](05-dotnet-sdk.md) | Consumer-transparent active-active routing and failover | 6 days | Blocked on Stage 0 decisions | Stage 4 |
| [6](06-packaging-and-upgrades.md) | Launch metadata and rolling-upgrade procedure | 5 days | Blocked on Stage 0 decisions | Stages 3-5 |
| [7](07-system-validation.md) | Failure scenarios, conformance, and final documentation | 5 days | Not started | Stages 1-6 |
|  | Total | 40 days |  |  |

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
- A request retried between the two endpoints of one machine remains the same
  registration proposal.
- Loss or rolling restart of one platform process does not prevent the other
  process from serving and completing new operations.
- All public APIs and non-obvious behavior are documented with the code that
  owns them.
- `task all` passes at the end of every implementation stage.

## Delivery rule

Stage 1 is complete. The unresolved items in Stage 0 block the later stages
named there. Each later stage may be
implemented as a small reviewable change, but the feature is not
production-ready until Stage 7 passes.
