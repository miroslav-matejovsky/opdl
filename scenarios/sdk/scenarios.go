package sdk

import "github.com/miroslav-matejovsky/opdl/scenarios/internal/runner"

// Scenarios is this category's contribution to the suite. The command collects
// it along with the other categories and hands the lot to the runner.
func Scenarios() runner.Set {
	return runner.Set{
		Package: "sdk",
		Scenarios: []runner.Scenario{
			{Name: "DotnetSDKEndToEnd", Func: DotnetSDKEndToEnd},
		},
	}
}
