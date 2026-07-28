package runner

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPrintSummary(t *testing.T) {
	t.Parallel()

	results := []*result{
		{
			name:     "smoke/BuildAndRunMinimumSite",
			ran:      true,
			passed:   true,
			duration: 1230 * time.Millisecond,
		},
		{
			name:     "redundancy/FailoverAndFailback",
			ran:      true,
			passed:   false,
			duration: 56950 * time.Millisecond,
		},
		{
			name:     "standby/WarmStandby",
			ran:      true,
			passed:   true,
			skipped:  true,
			duration: 0,
		},
		{
			name:   "resilience/UnranScenario",
			ran:    false,
			passed: false,
		},
	}

	var buf bytes.Buffer
	printSummary(&buf, results)

	expected := "--- scenarios summary ---\n" +
		"PASS  smoke/BuildAndRunMinimumSite (1s)\n" +
		"FAIL  redundancy/FailoverAndFailback (57s)\n" +
		"SKIP  standby/WarmStandby (0s)\n"

	require.Equal(t, expected, buf.String())
}

func TestPrintStartSummary(t *testing.T) {
	t.Parallel()

	allSets := []Set{
		{
			Package: "smoke",
			Scenarios: []Scenario{
				{Name: "BuildAndRunMinimumSite"},
				{Name: "BuildAndRunSingleMachine"},
			},
		},
		{
			Package: "resilience",
			Scenarios: []Scenario{
				{Name: "TwoMachineEventFabric"},
			},
		},
	}

	selectedSets := []Set{
		{
			Package: "smoke",
			Scenarios: []Scenario{
				{Name: "BuildAndRunMinimumSite"},
				{Name: "BuildAndRunSingleMachine"},
			},
		},
	}

	var buf bytes.Buffer
	printStartSummary(&buf, allSets, selectedSets)

	expected := "--- scenarios to run ---\n" +
		"RUN   smoke/BuildAndRunMinimumSite\n" +
		"RUN   smoke/BuildAndRunSingleMachine\n" +
		"SKIP  resilience/TwoMachineEventFabric\n"

	require.Equal(t, expected, buf.String())
}
