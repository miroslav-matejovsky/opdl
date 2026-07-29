// Package sdk holds the scenario that drives a running site through the
// generated .NET SDK, with nothing simulated on either side.
//
// The builder builds a machine from a blueprint, the platform runs as a real
// process, and a .NET consumer reaches it through the checked-in Kiota client.
// The SDK is the subject rather than the transport: what is being checked is
// that the platform's contract survives generation into models a consumer can
// use, which no Go test on either side of that boundary can tell you.
//
// It also closes the gap that made this category worth restoring. The .NET test
// project skips without an address to reach, and `dotnet test` exits 0 when it
// discovers nothing, so the repository gate can report success over a project
// that ran no assertion at all. This scenario asserts on the named tests having
// passed rather than on the exit code, and the project's test.runsettings fails
// a run that discovers nothing.
//
// The shared machinery is internal/harness; see its documentation for the
// scratch layout, the machine budget, and the failure diagnostics.
package sdk
