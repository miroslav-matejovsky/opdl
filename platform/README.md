# Platform Runtime

The `platform` directory contains the runtime assembled into every machine-specific OPDL binary. The command composes the embedded deployment descriptor, the Event Fabric and its site journal, the registration projection, services, and durable handler, and the public HTTP API.

Configuration lives in [`config`](config), and there is one source of it: the deployment descriptor compiled in at build time. Identity, endpoints, storage directories, the listener timeouts bounding each instance's API, and the Primary Ownership lease with its projection lag bound all come from it. There is no runtime configuration file: a launch decides only which of the machine's two instances the process is, through `-instance primary|standby`.

A site changes a setting by rebuilding from the blueprint, which is what it already did for every endpoint and every path. The listener timeouts are authored per instance in the blueprint's `api` blocks; the lag bound is authored on the machine's `standby.lease`, because it bounds a failover and a machine that deploys no standby trades ownership with nobody. A descriptor missing a required decision, or carrying a duration that is not positive, fails at startup rather than defaulting.

The internal packages keep the HTTP contract, registration decisions, and the event transport behind separate boundaries: the registration package is handed a publisher, a projector, and a handler, and never learns which transport carries them. For system contracts and architectural details, refer to the overview in [`docs`](../docs/README.md).

Each machine runs a Primary Instance and, when its descriptor's standby instance
is not disabled, a Standby Instance. Primary Ownership is a finite, renewable
lease recorded in a machine-wide file the descriptor names: the owner renews it
while Active, a Standby takes over once it lapses and the peer's health endpoint
reports it can no longer serve, and — under the Preferred Primary policy — an
Active Standby hands ownership back automatically once the returned Primary has
been continuously healthy for the stabilization window. Every transition is
automatic; there is no manual mode. Only the holder of Primary Ownership serves
domain operations. Every process writes mandatory local JSONL, which is what
supports diagnostics; nothing an instance writes locally grants ownership.

Both instances bind their own loopback API address and hold it for their whole
lifetime, and both move between Active and Passive in place, without the process
exiting. A Passive instance serves its health endpoints and `GET /instance`,
accepts no writes, and refuses domain operations with a `503` naming the
instance that owns. A transition swaps the handler behind a listener that is
already open, so an instance's address never moves and is never briefly free.

> **TODO — the Event Fabric is currently absent.** The NATS implementation of
> event storage and the Event Fabric was removed during the ongoing refactor, and
> a replacement distribution mechanism has not landed. Everything below about
> Event Fabric endpoints, `jetstream_store_dir`, storage selection, the site
> journal, and cross-machine coordination describes the design, not the code that
> is in the tree. Until the replacement lands, no deployment has event storage:
> `app.hasEventStorage` is hardcoded false, every instance takes the
> journal-less path, and registration and other domain operations are refused
> with a reason naming the deployment. The mandatory local JSONL record is
> unaffected and is the only event surface that currently works.

Each deployed instance has its own resolved Event Fabric endpoints and
`jetstream_store_dir`. Storage selection is per instance. Selected instances
bind their authored server endpoints and open their own JetStream stores;
client-only instances connect to those servers. Primary Ownership controls
domain activity, not Event Fabric membership.

There is no NATS monitoring listener. Each instance's own JSONL event record is
the supported local monitoring surface. Every process appends canonical
envelopes to `<data_dir>/events/events.jsonl`, and every operational question a
status file used to answer is a fact in it: which instance and PID stated it,
what it did (`platform.app.api_active`, `platform.redundancy.ownership_acquired`,
`platform.app.site_stopping`), and whether it is currently promotable
(`platform.app.failover_readiness_changed`, carrying projection progress against
the journal's high-water sequence, lag, and any error). Readiness is stated when
it changes rather than restated on a timer, so the last one an instance stated
is its current readiness. Process, ownership, and NATS lifecycle events
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
