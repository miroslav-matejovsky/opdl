// Package atomicfile provides atomic file writing operations.
//
// It allows callers to write data to a file such that concurrent or subsequent
// readers observe either the previous complete file or the newly written
// complete file, but never a partial write.
//
// To achieve atomic visibility, WriteFile writes data to a unique sibling
// temporary file in the same directory as the target path, applies the
// requested permissions, closes the file cleanly, and renames it over the
// target file. If any step fails before the replacement completes, the
// temporary file is removed automatically so no partial files leak.
//
// Atomic visibility guarantees:
// Atomic visibility is guaranteed only when the sibling temporary file and the
// target path reside on the same filesystem, as operating-system renames across
// filesystem boundaries are neither atomic nor guaranteed to succeed.
//
// Durability guarantees:
// WriteFile guarantees atomic visibility in directory entries, but it does not
// guarantee disk durability against sudden power loss or operating-system kernel
// crashes. Specifically, it does not invoke fsync (File.Sync) on the temporary
// file or the parent directory before replacing the target.
//
// Sharing conflicts:
// If a concurrent reader holds a short-lived handle on the target file during
// replacement, WriteFile retries for a bounded duration when it encounters
// sharing violations (ERROR_SHARING_VIOLATION or ERROR_ACCESS_DENIED).
// Persistent failures or other errors return immediately with the failing path
// and operation included in the error.
package atomicfile
