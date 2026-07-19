// Package filelock provides exclusive, crash-released, context-aware local file locks.
//
// A Lock represents an exclusive local file lock held through an operating-system
// file lock (LockFileEx on Windows, flock on Unix). The lock is released explicitly
// via Release or automatically when the holding process exits or crashes.
//
// Invariants:
//   - The lock path must reside on a local filesystem that supports the underlying
//     operating-system file locking primitives.
//   - On Unix systems, locks are advisory. All contending processes must use this
//     package or flock directly.
//   - A held lock has no lease or expiration. It remains held until released or until
//     the holding process exits.
//   - Context cancellation during Acquire stops waiting but does not release a lock
//     that is already held.
//   - Closing the process releases the operating-system lock automatically.
package filelock
