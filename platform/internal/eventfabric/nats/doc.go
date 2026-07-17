// Package nats is the Event Fabric's NATS JetStream adapter. It links a NATS
// server into the platform process and backs the OPDL Event Fabric with one
// file-backed JetStream site journal, so the platform ships as one binary and
// owns the journal's lifecycle.
//
// It implements eventfabric.Fabric and exposes no NATS type in that contract: a
// domain package publishes OPDL events and receives OPDL deliveries, and never
// sees a subject, stream, consumer, or connection.
//
// # Pinned versions
//
// The adapter pins exact module versions so a build is reproducible and the
// broker never floats:
//
//   - github.com/nats-io/nats-server/v2 v2.11.9 (embedded server, JetStream)
//   - github.com/nats-io/nats.go v1.46.1 (client and the jetstream API)
//
// # Topology
//
// Every node runs one embedded server. Server name, cluster name, addresses, and
// routes are derived from the deployment descriptor; see Config. JetStream
// storage runs only on the selected storage nodes — one for a site smaller than
// three machines, the first three by sorted machine name otherwise — with one or
// three stream replicas to match. Other nodes run Core NATS and route to the
// storage nodes, which avoids forming a two-member JetStream metadata group that
// would lose quorum when one member failed.
//
// # Journal
//
// The site journal is one file-backed stream named from the site scope, bound to
// the site's event subject filter, with limits retention and DiscardNew, no age
// or count deletion, and required byte and message-size limits. Startup creates
// it or validates an existing one against these requirements and refuses an
// incompatible journal rather than adopting or mutating it.
//
// # Publishing and delivery
//
// Publish stamps an event's envelope, validates it, and appends it to the
// journal synchronously through JetStream, returning the assigned sequence in
// the receipt. It sets the journal deduplication id from the event's stable
// publication identity when it has one. A projector runs an ordered consumer
// from the first retained event and continues live; a handler runs a durable
// pull consumer filtered by its routes with explicit acknowledgement and bounded
// redelivery. A decode failure, an unsupported event, or exhausted delivery
// stops the runner with an error so the node can be made unready rather than
// dropping an event.
//
// # Not wired into the runtime yet
//
// This stage builds and tests the adapter in isolation. Composing it into the
// platform runtime — replacing the Olric fabric and the JSONL recorder — is the
// runtime cutover in docs/plan/04-runtime-cutover.md.
package nats
