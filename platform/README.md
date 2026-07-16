# Platform Runtime

The `platform` directory contains the runtime assembled into every machine-specific OPDL binary. The command composes the embedded deployment descriptor, event sink, fabric adapter, registration service and reconciler, and public HTTP API.

Runtime identity and production endpoints come only from the embedded
descriptor. Production code has no default API or fabric ports. Configuration
may move sockets as a controlled operational fallback, select event storage,
and tune reconciliation, but it cannot change which project, site, machine, or
configured platform instance the binary represents.

The internal packages keep transport, registration decisions, distributed storage, and event recording behind separate boundaries. For system contracts and architectural details, refer to the overview in [`docs`](../docs/README.md).
