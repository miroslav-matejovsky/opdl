// Package eventlog appends every envelope stated by one process to its local
// JSONL event log.
//
// It does not filter by scope. Wider scope adds other destinations but the fact
// remains part of the stating instance's record. Writes are serialized and
// synced. The package is append-only because no platform behavior reads the log.
//
// See internal/instance/README.md for the instance storage model.
package eventlog
