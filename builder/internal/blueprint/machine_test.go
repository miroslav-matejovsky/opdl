package blueprint_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
)

func TestMachineListenersMustNotSharePort(t *testing.T) {
	tests := map[string]func(*blueprint.Machine){
		"standby api copies primary api": func(m *blueprint.Machine) { m.Standby.API.LocalPort = m.Primary.API.LocalPort },
	}
	for label, collide := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			collide(&p.Sites[0].Machines[0])
			require.ErrorContains(t, p.Validate(), "needs its own port")
		})
	}
}

func TestMachineEventstoreFileIsTheMachines(t *testing.T) {
	p := validProject()
	require.Equal(t, "D:/opdl/sensor/machine-events.jsonl", p.Sites[0].Machines[0].EventstoreFile)

	disableStandby(p)
	require.NoError(t, p.Validate())
	require.Equal(t, "D:/opdl/sensor/machine-events.jsonl", p.Sites[0].Machines[0].EventstoreFile,
		"a machine with one instance still keeps the machine's own account")
}

func TestProjectValidateEventstoreFileFailures(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*blueprint.Machine)
		errText string
	}{
		"blank": {
			func(m *blueprint.Machine) { m.EventstoreFile = "   " },
			"eventstore_file is required",
		},
		"padded": {
			func(m *blueprint.Machine) { m.EventstoreFile = " D:/opdl/sensor/machine-events.jsonl " },
			"leading or trailing whitespace",
		},
		"the primary instance's record": {
			func(m *blueprint.Machine) { m.EventstoreFile = m.Primary.EventlogFile },
			"eventstore_file and primary.eventlog_file are both",
		},
		"the standby instance's record spelled differently": {
			func(m *blueprint.Machine) { m.EventstoreFile = `D:\OPDL\SENSOR\standby\EVENTS.JSONL` },
			"eventstore_file and standby.eventlog_file are both",
		},
		"the lease file": {
			func(m *blueprint.Machine) { m.EventstoreFile = m.Standby.Lease.File },
			"eventstore_file and standby.lease.file are both",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			p := validProject()
			test.mutate(&p.Sites[0].Machines[0])
			require.ErrorContains(t, p.Validate(), test.errText)
		})
	}
}
