// Package runner runs registered scenarios through the standard library test
// runner, so scenarios keep t.Parallel, t.Cleanup, t.Run, and require while
// being driven from a command rather than from go test.
//
// A category package contributes a Set of Scenarios. The command collects the
// sets, applies package selection, and calls Run, which flattens them into
// testing.RunTests. Parallelism, the run filter, count, verbosity, and the
// timeout deadline all come from the test.* flags the command translates its own
// flags into before calling Run.
package runner
