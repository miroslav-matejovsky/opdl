// Package olric is the production fabric adapter, backed by an Olric member
// embedded in the platform process.
//
// It is the only package in the repository that imports Olric. Everything else
// depends on the fabric interface, so Olric is a decision runtime composition
// makes and not part of any contract: replacing it means writing a sibling of
// this package and changing one line of composition. Nothing in the fabric API
// exposes a DMap, an iterator, a member, or an address.
//
// # Bootstrap comes from the descriptor
//
// A machine's fabric topology is compiled in by the builder, so a member knows
// its site's membership at startup and discovers nothing. This adapter derives
// its addresses from those topology IPs on fixed ports: the client surface on
// ClientPort and membership on MemberlistPort. Peers are seeded from the
// descriptor's peer IPs on the same memberlist port. Config carries overrides
// for development and for scenarios that put several machines on one host; they
// move sockets only and never change who the machine is.
//
// # Startup and readiness
//
// Open returns once the member reports ready or fails. A peer that is not up
// yet does not prevent readiness: the member starts alone and the peers merge
// into the fabric when they arrive, which is what lets a site boot in any order.
// That path costs a memberlist join timeout, so StartTimeout must stay well
// above it; see DefaultStartTimeout. It follows that the member count observed
// at readiness is a reading at one instant, not the site's eventual size.
//
// # Backend behavior that is not a platform promise
//
// Olric partitions keys across members and can keep replicas of a partition.
// This adapter configures neither replication nor expiry, and storage is memory
// only: nothing is written to disk and nothing survives losing every member that
// holds a partition. None of that is a fabric guarantee, and none of it is
// service redundancy. The platform runs one fabric member per machine, with no
// primary/secondary instance, election, or fencing anywhere above it.
//
// # Tests
//
// Tests here start real members and bind real sockets, so they are skipped
// under "go test -short" and use dynamic ports. The shared fabric contract suite
// runs against this adapter and against the memory one, which is what proves the
// two keep the same promises.
package olric
