// Package jsonl implements a local file storage.Backend that appends stamped
// event envelopes as JSON Lines to the running instance's authored events file.
//
// The path comes from the deployment descriptor whole. The package composes no
// path of its own, so what a blueprint says an instance writes is what it
// writes.
//
// Writes are serialized and synced to disk on every Store call.
package jsonl
