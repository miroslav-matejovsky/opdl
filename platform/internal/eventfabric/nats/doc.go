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
//   - github.com/nats-io/nats-server/v2 v2.14.3 (embedded server, JetStream)
//   - github.com/nats-io/nats.go v1.52.0 (client and the jetstream API)
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
// # Ports and what listens on them
//
// A machine has two NATS ports and they carry different protocols:
//
//   - the client port serves the NATS client protocol. The active process, a
//     local standby following the journal, and every machine of the site that
//     does not store the journal all reach the Event Fabric through it. It is
//     bound on every storage node.
//   - the cluster port carries only the server-to-server route protocol between
//     storage nodes. It accepts no client connections, and it is bound only when
//     the resolved route list is non-empty. A configured cluster port is not
//     permission to bind it: on a site whose topology selects one storage node
//     there is no peer server, so binding it would open a port nothing can
//     connect to.
//
// There is no third port. No HTTP monitoring listener is configured: HTTPPort
// and HTTPSPort are left at zero, which is what makes the embedded server start
// none. The runtime reads connection state, journal high-water, projection
// progress, and lag through this adapter's own State and writes them to its
// per-process status files, so a second unauthenticated HTTP surface would add
// an open port without adding a signal.
//
// # One endpoint set per machine
//
// DefaultConfig takes no process role. A machine has one NATS topology, and the
// primary and standby are mutually exclusive owners of it.
//
// On a storage machine only the process holding the machine fence enables the
// embedded server and opens the JetStream directory. Its standby composes the
// same configuration and is then converted to client-only, which clears server
// ownership, the listener addresses, the routes, and the data directory while
// keeping the resolved server list whole. That list is what it reaches the
// journal through, and its first entry is the address the active process is
// serving on. Promotion closes that client composition before reopening the
// embedded server and retained directory under the fence, on the same addresses.
//
// Deriving a separate endpoint for the standby is what previously left it
// retrying against an address nothing was listening on, so no process role may
// select a different server list or listener address.
//
// # Journal
//
// The site journal is one file-backed stream named from the site scope, bound to
// the site's event subject filter, with limits retention and DiscardNew, no age
// or count deletion, and required byte and message-size limits. Startup creates
// it or validates an existing one against these requirements and refuses an
// incompatible journal rather than adopting or mutating it.
//
// Startup waits for the journal to become reachable, bounded by StartupTimeout.
// The waiting matters on a site with three storage nodes: their JetStream
// metadata group has no leader until a quorum of the three servers has found
// each other, and until it does the JetStream API does not answer at all, so a
// request against it times out rather than reporting a missing stream. Every
// storage node starts at once, so on a cold site that is the normal first
// answer. An incompatible journal is still refused immediately, because waiting
// cannot make it compatible.
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
// dropping an event. A missed pull-consumer heartbeat is recoverable: the NATS
// client issues another pull, the adapter records the degradation, and delivery
// continues on the same iterator.
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
