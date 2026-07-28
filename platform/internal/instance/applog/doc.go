// Package applog is one instance's structured application log.
//
// # What it is for
//
// The platform states facts as events (see platform/internal/events). An event
// is immutable, has a scope, and is consumed: the machine's store and, later,
// the site's distribution route it by that scope, and scenarios and tooling
// assert on it. Adding a diagnostic message to that flow would put something
// nobody may act on into a record everyone reads.
//
// An application log record is the other half. It says what the process was
// doing, in whatever detail a human needs while reading a failure, and nothing
// consumes it. The two are complementary and neither replaces the other, which
// is why an instance keeps them in separate files: an operator ships, keeps, and
// deletes them on different terms.
//
// # What it writes
//
// One JSON object per line, through log/slog's JSON handler, to the log_file the
// instance was built with. Every record carries the base attributes the process
// opened the logger with, so a line taken out of context still says which
// machine and which instance wrote it.
//
// Records are written to the process error stream as well as to the file. A
// packaged instance runs as a Windows Service and nothing reads that stream, but
// a developer running the binary and the scenario harness capturing a failed
// run's output both do, and neither has the log file to hand.
//
// # What it does not do
//
// There is no rotation, no retention, and no level configuration. The file grows
// until something outside the platform truncates or moves it, and the level is
// fixed at Info. Rotation is deliberately absent rather than pending: a rotating
// writer is a second answer to "where is this record" and is not worth having
// before an operator has asked for one.
package applog
