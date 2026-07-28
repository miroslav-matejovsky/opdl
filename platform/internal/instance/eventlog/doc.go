// Package eventlog implements the instance's local record: a file
// storage.Backend that appends stamped event envelopes as JSON Lines to the
// running instance's authored events file.
//
// The path comes from the deployment descriptor whole. The package composes no
// path of its own, so what a blueprint says an instance writes is what it
// writes.
//
// # It holds every scope
//
// The log is the instance's operational record: a logfile whose entries are an
// explicitly declared set, namely every event that happened on this instance.
// So it stores envelopes of every events.Scope and filters nothing. A
// site-scoped registration decision and a machine-scoped ownership handover are
// both things this instance did, and an operator reading one instance's file
// has to see them.
//
// Scope adds destinations elsewhere: the machine's shared store, the site's
// distribution. It never takes an event out of this file, which is why this
// backend has no scope configuration to get wrong.
//
// Writes are serialized and synced to disk on every Store call.
package eventlog
