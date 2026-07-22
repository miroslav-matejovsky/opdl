package runner

import "testing"

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
// It uses testing.MainStart to get real *testing.T values with working
// t.Parallel, t.Cleanup, t.Run, -run filtering, -timeout, -count, and verbose
// output. The deps adapter in deps.go satisfies the unexported testDeps
// interface for Go 1.26.5.
//
// The caller must have run testing.Init and flag.Parse first, so the test.*
// flags MainStart reads are registered and populated.
func Run(sets []Set) int {
	var tests []testing.InternalTest
	for _, set := range sets {
		for _, scenario := range set.Scenarios {
			tests = append(tests, testing.InternalTest{
				Name: set.Package + "/" + scenario.Name,
				F:    scenario.Func,
			})
		}
	}
	m := testing.MainStart(deps{}, tests, nil, nil, nil)
	return m.Run()
}
