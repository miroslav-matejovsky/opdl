// Package eventstore appends machine-scoped envelopes to the JSONL file shared
// by a machine's instances.
//
// Appender is the complete contract. There is no read API because no platform
// behavior consumes this store. Backend adapts an Appender to the common event
// publisher: it forwards machine scope and ignores other scopes. Direct Append
// calls reject non-machine envelopes.
//
// Both instances open the file. Ownership semantics normally leave one process
// stating machine facts at a time. Each accepted envelope is written as one
// synced line.
//
// See internal/machine/README.md for ownership and scope boundaries.
package eventstore
