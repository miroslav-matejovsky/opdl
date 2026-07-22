// Package scenarios runs black-box, end-to-end checks of the distribution line:
// it builds deployment packages, runs the resulting platform binaries as
// subprocesses, and checks their behavior from outside, never through internal
// APIs.
//
// The shared machinery lives in internal/harness, which builds a project
// in-process through builder/build and runs the machines it produces. A scenario
// drives the platform the way a customer would and proves the shipped artifacts
// rather than the code linked into a test binary. The moved helper packages
// (internal/procrun, internal/semaphore, internal/waitfor, internal/logscan,
// internal/processinfo) sit under internal too; only utils/testnet is still
// shared with other modules.
//
// The scenarios are still Go tests here, run under go test. A later stage moves
// each into a categorized package and runs them from cmd through the standard
// test runner, so parallelism, timeouts, and selection become the command's job.
// Until then, TestMain skips the whole suite under -short, so the unit gate
// compiles every scenario without running any.
package scenarios
