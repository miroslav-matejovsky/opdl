// Package memory is an in-process fabric adapter: one process, no network, no
// peers reachable but itself.
//
// It exists to make the fabric's semantics testable and to give the contract
// test suite a second implementation to hold the production adapter against. A
// fabric with only one adapter is a fabric whose promises are whatever that
// backend happens to do; this package is what keeps them honest, which is why it
// implements the atomicity and copy rules strictly rather than conveniently.
//
// It is not a production fallback. It shares nothing between processes, so a
// machine running it is a site of one that believes it is whole. Runtime
// composition never selects it.
package memory
