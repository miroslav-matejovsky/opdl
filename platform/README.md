# Platform Runtime

The `platform` directory contains the runtime assembled into every machine-specific OPDL binary. The command composes the embedded deployment descriptor, the Event Fabric and its site journal, the registration projection, services, and durable handler, and the public HTTP API.

Both tiers of configuration live in [`config`](config): the deployment descriptor compiled in at build time, and the TOML file read at startup. The effective configuration is their combination, which is why they are one package.

Runtime identity, endpoints, and Event Fabric topology come only from the descriptor. The configuration file places the journal's storage, retains operational events, and bounds startup and shutdown. It cannot move a socket, cannot set an instance's API address or runtime directory, and cannot change which project, site, or machine the binary represents: a configuration file that sets any of those is rejected at load time rather than ignored, because a setting that is silently dropped is indistinguishable from one that was applied.

The internal packages keep the HTTP contract, registration decisions, and the event transport behind separate boundaries: the registration package is handed a publisher, a projector, and a handler, and never learns which transport carries them. For system contracts and architectural details, refer to the overview in [`docs`](../docs/README.md).

Each machine runs a Primary Instance and, when its descriptor's standby instance
is not disabled, a Standby Instance. Only the holder of Primary Ownership opens
storage, handlers, readiness publication, and domain operations. The other
instance maintains a client-only projection and activates after ownership is
released. Local status files support service-manager handover and diagnostics but
never grant ownership.

Both instances bind their own loopback API address and hold it for their whole
lifetime. A Passive instance answers `GET /instance` about itself and refuses
domain operations with a `503` naming the instance that owns. Activation swaps the
handler behind a listener that is already open, so an instance's address never
moves and is never briefly free.

The two processes share one set of Event Fabric endpoints rather than owning one
each. The machine has one NATS client port and at most one cluster port, and
whichever instance holds ownership binds them; ownership is released only after
the active process has closed its embedded server. So the standby connects to the
address the active process is serving on, and promotion rebinds that same address
instead of moving the site onto a second one.

There is no NATS monitoring listener. The per-process status files are the
supported local monitoring surface: they report lifecycle state, projection
progress against the journal's high-water sequence, lag, promotability, and the
last error. Events are always written as JSON lines to stderr and can optionally
be retained under `operations.event_dir`. They cover process, ownership,
activation, server, connection, journal, projector, handler, readiness, API, and
shutdown transitions even when the Event Fabric is unavailable.

Local output and the site journal carry the same wrapper. Every event, wherever
it is read, is an `events.Envelope`: one occurrence identity and time, its type
and derived source, a schema version, a severity, the origin that stated it, the
optional causal links, and the typed payload as JSON. A reader decodes one shape
and never has to work out which writer produced a line first. What differs is
only where an event goes and what a failure means: a journal publication fails
the operation it belongs to, while a local record is best effort, because a
process that cannot describe itself must still run.
