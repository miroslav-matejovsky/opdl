package blueprint_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
)

func TestMachineListenersMustNotSharePort(t *testing.T) {
	tests := map[string]func(*blueprint.Machine){
		"standby api copies primary api": func(m *blueprint.Machine) { m.Standby.API.LocalPort = m.Primary.API.LocalPort },
		// A service's health check port is a listener on the machine like the
		// instances' API ports. The platform's APIs are on loopback and a service's
		// endpoint usually is not, but they are bound on one host either way.
		"health check copies the primary api port": func(m *blueprint.Machine) {
			m.Services[0].HealthCheck.Port = m.Primary.API.LocalPort
		},
		"health check copies the standby api port": func(m *blueprint.Machine) {
			m.Services[0].HealthCheck.Port = m.Standby.API.LocalPort
		},
		"two services share a health check port": func(m *blueprint.Machine) {
			m.Services = []blueprint.Service{
				validService("core-services", 9101),
				validService("alarm-service", 9101),
			}
		},
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
