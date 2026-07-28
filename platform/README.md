# Platform runtime

`platform` is the runtime compiled into every machine-specific OPDL binary.

## Configuration

The runtime has one configuration source: the deployment descriptor embedded at
build time. It contains machine identity, hosted services, the machine event
store, the mandatory Primary Instance, the optional Standby Instance, and the
lease present with that standby.

Each instance record has its own event log, state file, loopback API address,
listener timeouts, and Windows Service identity. A launch selects
`-instance primary|standby`; it does not supply identity or override the
descriptor.

## Runtime levels

| Level | Documentation | Packages |
| --- | --- | --- |
| Instance | [`internal/instance`](internal/instance/README.md) | event log and durable epoch |
| Machine | [`internal/machine`](internal/machine/README.md) | Primary Ownership and machine event store |
| Site | [`internal/site`](internal/site/README.md) | Event Fabric contract |

`internal/app` composes these levels. `internal/httpapi` is the public HTTP edge.
`internal/events` supplies the shared event contract.

## Current startup

The process loads the descriptor, resolves its fixed role, opens the instance
event log and machine event store, advances its epoch, binds its loopback API,
and enters ownership management.

Both instances keep their API bound. Only the Primary Ownership holder is
Active. Handler replacement changes the API surface without moving the address.

Site event distribution is not implemented. The active instance therefore
serves health and instance identity only; it has no domain operation to serve.
Local redundancy, instance event logging, and the machine event store continue
to work.

System design is documented in [`docs`](../docs/README.md).
