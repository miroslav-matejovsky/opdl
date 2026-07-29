# Machine level

A machine is one Windows host with a mandatory Primary Instance and an optional
Standby Instance. Both processes carry the same compiled machine identity.

## Packages

| Package | Responsibility |
| --- | --- |
| `redundancy` | Fixed roles, Primary Ownership, and active/passive sequencing. |
| `eventstore` | Shared append-only storage for machine-scoped envelopes. |
| `servicehealth` | Cancellable local HTTP probes, retry state, and target scheduling. |

## Primary Ownership

Active state comes from a finite, renewable lease in the machine-wide file named
by the descriptor. The owner renews it while Active. A Standby can take a lapsed
lease only when the peer is also unhealthy. An owner that cannot renew steps
down.

The preferred-primary policy hands ownership back after the Primary has remained
healthy for the configured stabilization interval. Roles never swap: an Active
Standby remains the Standby Instance.

`redundancy.ManageOwnership` sequences Passive and Active callbacks. Passive
returns before Active starts, and ownership is released only after Active
returns. The package does not know which services those callbacks compose.

## Machine event store

Every machine has one `eventstore_file` in the blueprint, resolved as
`machine_events_file` in its descriptor. Both instances open it. Machine-scoped
ownership and activation facts form one account across failover and restart.

The store is append-only JSONL and syncs each accepted line. It has no read API
because no platform behavior consumes it. The backend used by the common event
publisher ignores non-machine scopes; a direct `Appender` call rejects them.

Instance-scoped lease attempts, waiting, and declined promotions remain only in
the stating process's event log. See [Events](../../../docs/02-events.md).

Local ownership does not depend on site distribution.

## Service health

Every running platform instance probes all configured services on its machine,
independent of Active or Passive ownership state. A success recovers
immediately. Configured consecutive failures are required before Unhealthy.
Each completed attempt emits the current stable observation. Distribution and
site reduction remain site-level responsibilities.
