// Package filelock provides exclusive, crash-released, context-aware local file locks.
//
// A Lock represents an exclusive local file lock held through LockFileEx. The
// lock is released explicitly via Release or automatically when the holding
// process exits or crashes.
//
// Invariants:
//   - The lock path must reside on a local filesystem that supports LockFileEx.
//     A network filesystem such as SMB may not enforce it.
//   - A held lock has no lease or expiration. It remains held until released or until
//     the holding process exits.
//   - Context cancellation during Acquire stops waiting but does not release a lock
//     that is already held.
//   - Closing the process releases the operating-system lock automatically.
package filelock
