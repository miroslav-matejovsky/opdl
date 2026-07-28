// Package natsserver runs the embedded NATS server one platform instance owns.
//
// The server is instance-level infrastructure: each of a machine's two
// processes starts its own from its own descriptor record, holds it for its
// whole lifetime whether it is Active or Passive, and shares nothing with the
// other process. That is the same rule the instance's event log, application
// log, and API listener follow.
//
// It is started with JetStream off. Without JetStream the server carries
// messages between whoever is connected to it right now and stores nothing, so
// nothing here provides the durable replay and acknowledgement
// site/eventfabric.Consumer describes. Turning JetStream on is what that will
// need; until then this is a transport and not a journal.
//
// # What it binds, and what it does not
//
// The only client of this server is the platform's own, in this process. It is
// handed a connection by InProcessConn and dials nothing, so no client port is
// authored anywhere: there is no port for an outside client because there is no
// outside client.
//
// What it binds on purpose is the route listener, on the address the descriptor
// resolved from the instance's authored nats cluster_port joined to the
// machine's ip. It is off loopback deliberately: the cluster spans the site's
// machines, so a member has to be reachable from another host.
//
// A client listener is still bound, on loopback and on an ephemeral port,
// because the server will not start routing until one is up. Nothing dials it
// and no deployment states it. See Start.
//
// # The site's cluster
//
// Every deployed instance at a site is a member of one cluster: both instances
// of every machine, primaries and standbys alike. The descriptor carries each
// member's peers as routes, resolved by the builder from the site's shape, and
// the cluster is named at the site. A server accepts a route only from a peer
// naming its cluster, so two sites of one project form two clusters.
//
// The routes are mutual, which is what makes start order irrelevant: each member
// dials every other member, NATS keeps one connection per pair, and a peer that
// is not listening yet is retried. Reaching the cluster is therefore not part of
// coming up. Start returns once this server is ready, whether or not any peer
// is.
//
// # Logging
//
// The server writes nothing on its own. Its account goes through a logger onto
// the instance's application log, which is what keeps a listener it could not
// bind from failing silently: NATS reports that by calling Fatalf and returning,
// so a server with no logger installed would time out with no reason given.
package natsserver
