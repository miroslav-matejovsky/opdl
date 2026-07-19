// Package processtree starts child processes inside operating-system process
// containers, terminates them gracefully when supported, and force-kills all
// descendants during cleanup.
//
// On Windows, child processes are created suspended and assigned to a job object
// configured with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE before their threads are
// resumed. This ensures that no descendant process can outlive the owner handle.
// On Unix systems, processes are created in a distinct process group (PGID) and
// signaled by group ID so that descendants are targeted together.
//
// The Owner type returned by Start is concurrency safe and its Stop, Kill, and
// Close operations are idempotent.
package processtree
