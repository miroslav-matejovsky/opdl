// Package runner runs registered scenarios through the standard library test
// runner, so scenarios keep t.Parallel, t.Cleanup, t.Run, and require while
// being driven from a command rather than from go test.
//
// A category package contributes a Set of Scenarios. The command collects the
// sets, narrows them with Select, and calls Run, which flattens what is left
// into testing.MainStart under the name category/Scenario. List prints the same
// names, so what -list shows is what -run matches. Parallelism, the run filter,
// count, verbosity, and the timeout deadline all come from the test.* flags the
// command translates its own flags into before calling Run.
//
// The testDeps adapter in deps.go satisfies the unexported testing.testDeps
// interface by exploiting the fact that corpusEntry is a type alias to an
// anonymous struct, so the signatures match structurally. The adapter is
// Go-version-specific; it was written against Go 1.26.5.
package runner
