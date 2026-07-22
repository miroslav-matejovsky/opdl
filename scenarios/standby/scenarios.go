package standby

import "github.com/miroslav-matejovsky/opdl/scenarios/internal/runner"

// category is the name the runner prefixes this package's scenarios with, so
// -run standby selects all of them.
const category = "standby"

// Scenarios is this category's contribution to the suite. The command collects
// it along with the other categories and hands the lot to the runner.
func Scenarios() runner.Set {
	return runner.Set{
		Package: category,
		Scenarios: []runner.Scenario{
			{Name: "ManifestArgumentsMatchRuntime", Func: ManifestArgumentsMatchRuntime},
			{Name: "WarmStandbyFailoverAndPreferredPrimary", Func: WarmStandbyFailoverAndPreferredPrimary},
		},
	}
}
