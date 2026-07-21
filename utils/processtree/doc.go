// Package processtree starts child processes inside Windows job objects,
// terminates them gracefully, and force-kills all descendants during cleanup.
//
// Child processes are created suspended and assigned to a job object configured
// with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE before their threads are resumed. This
// ensures that no descendant process can outlive the owner handle. Each child is
// also its own process group leader, so Stop can signal it with a console
// control event without reaching the parent.
//
// The Owner type returned by Start is concurrency safe and its Stop, Kill, and
// Close operations are idempotent.
package processtree
