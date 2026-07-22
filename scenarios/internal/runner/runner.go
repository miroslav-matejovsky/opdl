package runner

import (
	"regexp"
	"testing"
)

// Scenario is one black-box scenario: a name and the function that runs it. The
// function keeps the shape of a Go test, so a scenario uses t.Parallel,
// t.Cleanup, t.Run, and require exactly as it would under go test.
type Scenario struct {
	Name string
	Func func(*testing.T)
}

// Set is the scenarios one category package contributes. Package selection
// (Select) works on whole Sets; -run filters individual scenarios by name.
type Set struct {
	Package   string
	Scenarios []Scenario
}

// Run flattens the sets into the standard library test runner and returns a
// process exit code: 0 when every scenario passed, 1 otherwise.
//
// It uses testing.RunTests rather than testing.MainStart. MainStart's first
// parameter is the unexported testing.testDeps interface, whose fuzzing methods
// name the unexported testing.corpusEntry type, so testDeps cannot be
// implemented from another package and MainStart cannot be called from here.
// RunTests needs only a match function and the test list, and it still gives
// real *testing.T values with working t.Parallel, the -run filter, -count, and
// verbose output.
//
// The caller must have run testing.Init and flag.Parse first, so the test.*
// flags RunTests reads (test.parallel, test.run, test.count, test.v, and the
// test.timeout deadline it computes) are registered and populated.
func Run(sets []Set) int {
	var tests []testing.InternalTest
	for _, set := range sets {
		for _, scenario := range set.Scenarios {
			tests = append(tests, testing.InternalTest{Name: scenario.Name, F: scenario.Func})
		}
	}
	if testing.RunTests(matchString, tests) {
		return 0
	}
	return 1
}

// matchString is the -run matcher. The testing matcher splits both the pattern
// and the test name on "/" and calls this once per segment, so a single regexp
// match per segment is exactly what a go-test-generated binary provides. An
// empty pattern matches everything, which is the default (run all scenarios).
func matchString(pattern, name string) (bool, error) {
	return regexp.MatchString(pattern, name)
}
