// Package jsonl is a file-backed events.Sink that appends each event as one
// compact JSON object per line.
//
// It is the platform's only durable event backend today, and it is a
// development and scenario backend, not a production one. A database or
// distributed backend would be a sibling implementation of events.Sink, so
// nothing outside this package knows events are stored as files.
//
// # The node is in the file name
//
// The events.Node an event is about is constant for a whole process run, so
// this sink states it once, in its file name, rather than repeating five fields
// on every line:
//
//	events-<project>-<environment>-<site>-<machine>-<role>.jsonl
//
// One file therefore holds the events of exactly one node, and a directory that
// collects several nodes keeps them apart without any per-record identity. A
// sink that pools nodes into one stream, as a production backend would, has to
// carry events.Node per record instead; that is a property of this sink, not of
// the event model.
//
// Characters that are not portable in a file name are replaced, so a node whose
// identifiers are unusual still records. Two nodes whose identifiers differ only
// in those characters would share a file, which is acceptable for a backend
// that will not run in production.
//
// # Durability
//
// Every accepted record is flushed before Append returns, so a reader that
// polls the file observes an event as soon as the emitting operation completes.
// Flushing is not the same as durability against an abrupt process kill: the
// data has left the process, but operating system buffers may still lose it if
// the machine fails. A reader that needs an event should read it while the
// process is alive.
//
// The sink deliberately has no read side, rotation, or retention: one run
// appends to one file, and it grows. Those concerns arrive with the operational
// surface that needs them.
package jsonl
