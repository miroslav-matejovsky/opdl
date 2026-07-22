// Package runner runs registered scenarios through the standard library test
// runner, so scenarios keep t.Parallel, t.Cleanup, t.Run, and require while
// being driven from a command rather than from go test.
//
// A category package contributes a Set of Scenarios. The command collects the
// sets, applies package selection, and calls Run, which flattens them into
// testing.MainStart. Parallelism, the run filter, count, verbosity, and the
// timeout deadline all come from the test.* flags the command translates its own
// flags into before calling Run.
//
// The testDeps adapter in deps.go satisfies the unexported testing.testDeps
// interface by exploiting the fact that corpusEntry is a type alias to an
// anonymous struct, so the signatures match structurally. The adapter is
// Go-version-specific; it was written against Go 1.26.5.
package runner
