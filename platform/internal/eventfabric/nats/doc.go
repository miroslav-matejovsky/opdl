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
// The site's storage nodes are selected by sorted machine name — one for a site
// smaller than three machines, the first three otherwise — with one or three
// journal replicas to match. This avoids a two-member JetStream metadata group,
// which would lose quorum whenever either member failed.
//
// Only a storage node runs a server, and the site's cluster is exactly the
// storage nodes. Every other machine of the site runs nothing and reaches the
// journal as a client of them. That is not a simplification: NATS sizes a
// JetStream metadata group from a server's configured routes rather than from
// the servers that enable JetStream (see server/raft.go, "Determining expected
// peer size"), so a server in the cluster that stores nothing still joins the
// group deciding whether the site can write, while adding nowhere to write to.
// A single storage node with a route to a routing-only peer never elects a
// metadata leader, and every JetStream call times out. One server and a client
// is what keeps a two-machine site working while its second machine is down.
//
// A machine that does not store the journal therefore depends on one that does.
// It retries the connection and waits for the journal to exist, both bounded by
// StartupTimeout, and does not open if no storage node answers.
//
// Server name, cluster name, addresses, routes, and servers are all derived from
// the deployment descriptor; see Config.
//
// On a storage machine, only the process holding the machine fence enables the
// embedded server and opens the JetStream directory. Its standby uses the same
// server list in client-only mode. Promotion closes that client composition
// before reopening the embedded server and retained directory under the fence.
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
// # What the adapter does not decide
//
// Open returns a fabric that is usable, not a node that is ready, and it states
// nothing in the journal. Readiness is a conclusion about a node's projections
// and handlers, which only runtime composition can reach; Close is silent for the
// same reason, since a transport closing itself is not evidence a node stopped
// cleanly. Info reports what this node is, and internal/app publishes the ready
// and stopping facts.
package nats
