// Package eventstore is the machine level's event store: the one place a
// machine's story is kept whole, rather than split across whichever of its two
// instances happened to be running.
//
// # What it is for
//
// An instance's own record (internal/instance/eventlog) is private to one
// process, and a machine's ownership moves between two of them. So the account
// of what happened to the machine — who owned it, when it changed hands, how
// each activation ended — is spread over two files, neither of which is the
// machine's. This store is that account in one file, named by the machine's
// blueprint and shared by both of its instances.
//
// # Level is not scope
//
// A package's level says where its code belongs, not what scope its events
// carry. internal/machine/redundancy states machine-scoped ownership facts and
// instance-scoped ones about what this process did about them; a site package
// may state facts of any of the three levels. So this store does not hold "the
// machine package's events". It holds machine-scoped envelopes, whichever
// package stated them, and the sorting is per envelope:
//
//   - Append is the deliberate call. A caller reaching for the machine's store
//     with an envelope of another level has made a composition mistake, and
//     gets ErrScope rather than a store with one process's story in it.
//   - Backend is the routing call. A publisher hands every envelope it stamps
//     to every backend it has, so most of what arrives there is not this
//     store's; the machine's are kept and the rest pass by.
//
// # It is append-only
//
// There is no Reader, and dropping it was the point rather than an omission.
// Nothing in the platform reads a machine store: the passive instance does not
// need one to take over, and no query is answered from it. A read contract with
// no consumer would be a guess at what a future consumer wants, kept alive by
// tests written to exercise it — and it is the expensive half, because
// replay-then-follow over a file another process is appending to is where the
// hard cases live. The store is written for the reader it does have, which is
// an operator with a text editor and whatever tooling reads a JSON Lines file.
//
// The same holds one level down: the instance eventlog is append-only for the
// same reason and has been from the start.
//
// This is what a machine-level consumer costs when one is named: an interface
// method, an implementation of it, and its tests. It is not what the store's
// shape costs, because a store that already keeps every machine-scoped envelope
// in order has everything such a consumer would read.
//
// # Who writes
//
//	instance | write-only | one process appends, nobody reads back
//	machine  | write-only | both instances hold it open; only the owner has a machine fact to state
//	site     | read/write | the Active instance publishes, every machine reads (internal/site/eventfabric)
//
// Both of a machine's instances open the store for the life of the process,
// because a fact does not wait for a composition to be built before it happens.
// In practice one writer is in it at a time anyway: every machine-scoped event
// is stated by the instance that holds Primary Ownership. Where that is not
// guaranteed — the moment ownership changes hands — the file still holds whole
// lines, because an append is one locked, synced write of one complete line.
//
// # The file implementation
//
// Open backs the store with a JSON Lines file, one envelope per line, appended
// and fsynced in the order stated. The store the machine's instances share is
// expected to become SQLite; nothing above this package should have to change
// when it does, which is what Appender and the contract test suite are for.
package eventstore
