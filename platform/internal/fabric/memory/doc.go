// Package memory is an in-process fabric adapter: one process, no network, no
// peers reachable but itself.
//
// It exists to make the fabric's semantics testable and to give the contract
// test suite a second implementation to hold the production adapter against. A
// fabric with only one adapter is a fabric whose promises are whatever that
// backend happens to do; this package is what keeps them honest, which is why it
// implements the atomicity and copy rules strictly rather than conveniently.
//
// # Several members in one process
//
// Open builds a member of a site of its own. Site builds members that share
// their collections, so a test can run the machines of one site together and
// have them really share state: one machine's write is another's read, and each
// keeps its own descriptor identity, membership, and lifecycle. That makes
// site-wide behavior testable in one process, at one adapter's speed, without a
// network.
//
// A member is one platform instance, so a redundant machine's primary and
// secondary are two members. Opening one machine's descriptor twice on a Site,
// once as primary and once as secondary, gives a test both instances of that
// machine as independent handles over the same shared collections, each marking
// its own instance as Self.
//
// Shared collections do not make the members reachable to each other. They never
// connected, so State still reports what it can honestly see, and a shared site
// of two reads as disconnected.
//
// It is not a production fallback. It shares nothing between processes, so a
// machine running it is a site of one that believes it is whole. Runtime
// composition never selects it.
package memory
