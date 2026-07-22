package runner

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
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

// Select narrows the sets to the categories the caller asked for. Every category
// is enabled by default, so an empty only and an empty skip return the sets
// unchanged.
//
// only and skip are mutually exclusive, and an unknown name in either is an
// error rather than a no-op: a typo that silently ran the whole suite, or
// silently skipped nothing, is worse than a failed invocation. The names are
// validated against the sets passed in, so this list has one source of truth.
//
// Selection is whole categories. Narrowing within one is what -run is for, and
// the two compose.
func Select(sets []Set, only, skip []string) ([]Set, error) {
	if len(only) > 0 && len(skip) > 0 {
		return nil, errors.New("only and skip are mutually exclusive: name the categories to run, or the ones to leave out")
	}
	if err := known(sets, "only", only); err != nil {
		return nil, err
	}
	if err := known(sets, "skip", skip); err != nil {
		return nil, err
	}

	var keep func(string) bool
	switch {
	case len(only) > 0:
		keep = func(name string) bool { return slices.Contains(only, name) }
	case len(skip) > 0:
		keep = func(name string) bool { return !slices.Contains(skip, name) }
	default:
		return sets, nil
	}

	selected := make([]Set, 0, len(sets))
	for _, set := range sets {
		if keep(set.Package) {
			selected = append(selected, set)
		}
	}
	return selected, nil
}

// known reports the first name that is not one of the sets' categories.
func known(sets []Set, flagName string, names []string) error {
	for _, name := range names {
		if !slices.ContainsFunc(sets, func(s Set) bool { return s.Package == name }) {
			return fmt.Errorf("%s: no such category %q; the suite has %s",
				flagName, name, strings.Join(categories(sets), ", "))
		}
	}
	return nil
}

// categories names the sets, in the order they would run.
func categories(sets []Set) []string {
	names := make([]string, 0, len(sets))
	for _, set := range sets {
		names = append(names, set.Package)
	}
	return names
}

// List writes the sets and the scenarios in them, one scenario per line, under
// the name -run matches against.
//
// A write error is dropped: this prints a listing to a terminal, and there is
// nothing a caller could do about a failure to do so that is better than
// carrying on.
func List(w io.Writer, sets []Set) {
	for _, set := range sets {
		_, _ = fmt.Fprintln(w, set.Package)
		for _, scenario := range set.Scenarios {
			_, _ = fmt.Fprintf(w, "\t%s/%s\n", set.Package, scenario.Name)
		}
	}
}
