# Platform Runtime

The `platform` directory contains the runtime assembled into every machine-specific OPDL binary. The command composes the embedded deployment descriptor, the Event Fabric and its site journal, the registration projection, services, and durable handler, and the public HTTP API.

Runtime identity and Event Fabric topology come only from the embedded descriptor. Configuration places the journal's storage, sets the public API address, and bounds startup and shutdown. It cannot move a NATS socket and cannot change which project, site, or machine the binary represents: a configuration file that sets a socket, route, or server list is rejected at load time rather than ignored, because a setting that is silently dropped is indistinguishable from one that was applied.

The internal packages keep the HTTP contract, registration decisions, and the event transport behind separate boundaries: the registration package is handed a publisher, a projector, and a handler, and never learns which transport carries them. For system contracts and architectural details, refer to the overview in [`docs`](../docs/README.md).

Each machine runs the manifest's preferred primary and, when its descriptor's
standby slot is not disabled, an optional standby. Only the local ownership holder
opens storage, handlers, readiness publication, and HTTP. The other process
maintains a client-only projection and activates after ownership is released.
Local status files support service-manager handover and diagnostics but never
grant ownership.

The two processes share one set of Event Fabric endpoints rather than owning one
each. The machine has one NATS client port and at most one cluster port, and
whichever instance holds ownership binds them; ownership is released only after
the active process has closed its embedded server. So the standby connects to the
address the active process is serving on, and promotion rebinds that same address
instead of moving the site onto a second one.

There is no NATS monitoring listener. The per-process status files are the
supported local monitoring surface: they report lifecycle state, projection
progress against the journal's high-water sequence, lag, promotability, and the
last error. Structured operational events are always written as JSON lines to
stderr and can optionally be retained under `operations.event_dir`. They cover
process, ownership, activation, server, connection, journal, projector, handler,
readiness, API, and shutdown transitions even when the Event Fabric is
unavailable. See the [operations guide](../docs/plan/operations/README.md).
