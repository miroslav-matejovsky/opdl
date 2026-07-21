// Package winmutex provides exclusive, machine-wide, kernel-owned locks built on
// Windows named mutexes.
//
// A Mutex is an ownership primitive, not a lease. Exactly one process holds it at
// a time; it is released explicitly, or abandoned by the kernel when the holding
// process exits for any reason. There is no expiry and no preemption, so a stalled
// holder blocks a waiter rather than allowing a second owner.
//
// # Why a kernel object rather than a file lock
//
// A named mutex is identified by a name in a machine-wide namespace, not by a
// filesystem path. Two processes exclude each other because they use the same
// name, regardless of installation path, working directory, or configuration, and
// no network filesystem can weaken the semantics.
//
// # Thread affinity is a correctness requirement
//
// Windows ties mutex ownership to the *thread* that acquired it, not the process.
// If that thread exits while the process keeps running, the kernel abandons the
// mutex and another process may acquire it while the first still holds whatever
// the mutex was protecting. That is a split-brain vector an OS file lock, which is
// held through a process handle, structurally cannot have.
//
// Go schedules goroutines across OS threads, so this is easy to hit by accident.
// Every Mutex therefore owns one dedicated OS thread, pinned with
// runtime.LockOSThread and deliberately never unlocked, and every acquire, wait,
// and release is executed on it. The thread lives until Close. Callers never
// observe it.
//
// This property is the reason the package exists rather than callers using
// golang.org/x/sys/windows directly.
//
// # Namespace
//
// Names are always created in the Global\ kernel namespace. Local\ names are
// per-session, so a service in session 0 and an interactive process in another
// session would each get their own object and would not exclude each other. There
// is deliberately no fallback and no configuration option: a process that cannot
// use Global\ fails rather than silently getting a weaker guarantee.
//
// Creating a Global\ object requires SeCreateGlobalPrivilege, which services,
// administrators, and interactive logons hold by default.
//
// # Security
//
// A created object is given an explicit DACL granting only LocalSystem, the
// Administrators group, and the account the creating process runs as. The default
// security descriptor is not accepted.
//
// A local process may still create an object with the same name first, which
// denies the platform its ownership. That is a local denial of service equivalent
// to one holding a lock file, and it is reported rather than silently retried:
// Open fails when a name exists but cannot be opened. Verifying the security
// descriptor of an object this process did not create is not implemented; Existed
// reports the condition so a caller can log it.
//
// Invariants:
//   - Ownership is exclusive across processes on one machine.
//   - Ownership has no lease and no expiry.
//   - Acquire and Release must be called from the same Mutex value; the package
//     routes both onto its own thread, so callers need no thread discipline.
//   - Release is only valid while held, and Close is only valid once not held.
//   - Canceling Acquire stops waiting; it never releases ownership already held.
package winmutex
