// Package jsonl implements a local file storage.Backend that appends stamped
// event envelopes as JSON Lines to <data_dir>/events/events.jsonl.
//
// Writes are serialized and synced to disk on every Store call.
package jsonl
