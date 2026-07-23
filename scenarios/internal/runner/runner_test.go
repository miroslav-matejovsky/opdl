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
			name:     "registration/BuildAndRunMinimumSite",
			ran:      true,
			passed:   true,
			duration: 1230 * time.Millisecond,
		},
		{
			name:     "registration/TwoMachineRegistration",
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
			name:   "nats/UnranScenario",
			ran:    false,
			passed: false,
		},
	}

	var buf bytes.Buffer
	printSummary(&buf, results)

	expected := "--- scenario summary ---\n" +
		"PASS  registration/BuildAndRunMinimumSite (1.23s)\n" +
		"FAIL  registration/TwoMachineRegistration (56.95s)\n" +
		"SKIP  standby/WarmStandby (0s)\n"

	require.Equal(t, expected, buf.String())
}

func TestPrintStartSummary(t *testing.T) {
	t.Parallel()

	allSets := []Set{
		{
			Package: "registration",
			Scenarios: []Scenario{
				{Name: "BuildAndRunMinimumSite"},
				{Name: "TwoMachineRegistration"},
			},
		},
		{
			Package: "nats",
			Scenarios: []Scenario{
				{Name: "TwoMachineEventFabric"},
			},
		},
	}

	selectedSets := []Set{
		{
			Package: "registration",
			Scenarios: []Scenario{
				{Name: "BuildAndRunMinimumSite"},
				{Name: "TwoMachineRegistration"},
			},
		},
	}

	var buf bytes.Buffer
	printStartSummary(&buf, allSets, selectedSets)

	expected := "--- scenarios to run ---\n" +
		"RUN   registration/BuildAndRunMinimumSite\n" +
		"RUN   registration/TwoMachineRegistration\n" +
		"SKIP  nats/TwoMachineEventFabric\n"

	require.Equal(t, expected, buf.String())
}
