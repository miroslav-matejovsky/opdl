# Instance level

An instance is one platform process in the fixed `primary` or `standby` role.
Each instance owns its loopback API address and local files. Nothing in this
directory is shared between the two processes on a machine.

## Packages

| Package | Responsibility |
| --- | --- |
| `eventlog` | Appends every envelope stated by the process to its JSONL event log. |
| `state` | Persists the instance epoch across restart and activation. |

## Event log

The event log is the process's operational record. It receives instance-,
machine-, and site-scoped facts stated by that process. Scope adds wider
destinations but never removes this local destination.

The log is append-only, writes one canonical envelope per line, and syncs each
write. The platform has no read API for it. Operators, scenarios, and external
tools are its readers.

## State and epoch

The state file contains a monotonic epoch plus separate process-start and
activation counters. The epoch advances before a process incarnation runs and
before an activation serves. Stepping down does not advance it.

Each advance is written atomically before it is returned. An invalid existing
state file fails startup instead of resetting the counter. Primary and standby
epochs are independent.

The shared lease and machine event store belong to the
[machine level](../machine/README.md).
