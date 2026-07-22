# Platform Runtime

The `platform` directory contains the runtime assembled into every machine-specific OPDL binary. The command composes the embedded deployment descriptor, the Event Fabric and its site journal, the registration projection, services, and durable handler, and the public HTTP API.

Both tiers of configuration live in [`config`](config): the deployment descriptor compiled in at build time, and the TOML file read at startup. The effective configuration is their combination, which is why they are one package.

Runtime identity, endpoints, storage directories, and Event Fabric topology come only from the descriptor. The configuration file supplies runtime-only timeouts and NATS credentials. It cannot move a socket, select a backend, change a storage path, or change which project, site, or machine the binary represents. Unknown settings are rejected rather than ignored.

The internal packages keep the HTTP contract, registration decisions, and the event transport behind separate boundaries: the registration package is handed a publisher, a projector, and a handler, and never learns which transport carries them. For system contracts and architectural details, refer to the overview in [`docs`](../docs/README.md).

Each machine runs a Primary Instance and, when its descriptor's standby instance
is not disabled, a Standby Instance. Every process writes mandatory local JSONL
and maintains an Event Fabric projection. An instance selected as a storage node
also runs its own NATS server and JetStream store. Only the holder of Primary
Ownership opens durable handlers, publishes readiness, and serves domain
operations. Local status files support service-manager handover and diagnostics
but never grant ownership.

Both instances bind their own loopback API address and hold it for their whole
lifetime. A Passive instance answers `GET /instance` about itself and refuses
domain operations with a `503` naming the instance that owns. Activation swaps the
handler behind a listener that is already open, so an instance's address never
moves and is never briefly free.

Each deployed instance has its own resolved Event Fabric endpoints and
`jetstream_store_dir`. Storage selection is per instance. Selected instances
bind their authored server endpoints and open their own JetStream stores;
client-only instances connect to those servers. Primary Ownership controls
domain activity, not Event Fabric membership.

There is no NATS monitoring listener. The per-process status files are the
supported local monitoring surface: they report lifecycle state, projection
progress against the journal's high-water sequence, lag, promotability, and the
last error. Every process appends canonical envelopes to
`<data_dir>/events/events.jsonl`. Process, ownership, and NATS lifecycle events
use this local record, so they remain available when the Event Fabric is
unavailable. Site facts are synchronously fanned out to the same JSONL record and
the NATS journal.

Local output and the site journal carry the same wrapper. Every event, wherever
it is read, is an `events.Envelope`: one occurrence identity and time, its type
and derived source, a schema version, a severity, the origin that stated it, the
optional causal links, and the typed payload as JSON. A reader decodes one shape
and never has to work out which writer produced a line first. What differs is
only where an event goes and what a failure means. Domain operations propagate
publication failures. Startup and shutdown return or join them. Background
callbacks report them to stderr without trying to publish another event through
the failed pipeline.
