package blueprint_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
)

func TestMachineListenersMustNotSharePort(t *testing.T) {
	tests := map[string]func(*blueprint.Machine){
		"standby api copies primary api":   func(m *blueprint.Machine) { m.Standby.API.LocalPort = m.Primary.API.LocalPort },
		"standby nats copies primary nats": func(m *blueprint.Machine) { m.Standby.NATS.ClusterPort = m.Primary.NATS.ClusterPort },
	}
	for label, collide := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			collide(&p.Sites[0].Machines[0])
			require.ErrorContains(t, p.Validate(), "needs its own port")
		})
	}
}

// TestServiceMayNotBeProbedOnAPlatformPort checks a service's health check
// cannot be aimed at an endpoint the platform binds. Probing one would report
// the platform instance's own health under a service's name, which is the one
// answer a service monitor must never give.
func TestServiceMayNotBeProbedOnAPlatformPort(t *testing.T) {
	tests := map[string]func(*blueprint.Machine){
		"primary api": func(m *blueprint.Machine) { m.Services[0].HealthCheck.Port = m.Primary.API.LocalPort },
		"standby api": func(m *blueprint.Machine) { m.Services[0].HealthCheck.Port = m.Standby.API.LocalPort },
		"primary nats": func(m *blueprint.Machine) {
			m.Services[0].HealthCheck.Port = m.Primary.NATS.ClusterPort
		},
		"standby nats": func(m *blueprint.Machine) {
			m.Services[0].HealthCheck.Port = m.Standby.NATS.ClusterPort
		},
	}
	for label, collide := range tests {
		t.Run(label, func(t *testing.T) {
			p := validProject()
			collide(&p.Sites[0].Machines[0])
			require.ErrorContains(t, p.Validate(), "cannot be probed on a port the platform binds")
		})
	}
}

// TestServicesMayShareAProbePort checks two service identities can be hosted by
// one HTTP listener and told apart by their paths. A service's health endpoint
// is a target the platform probes, not a listener it binds, so the machine-wide
// listener rule does not apply to it.
func TestServicesMayShareAProbePort(t *testing.T) {
	p := validProject()
	alarm := validService("alarm-service", 9101)
	alarm.HealthCheck.Path = "/alarm/health"
	reporting := validService("reporting-service", 9101)
	reporting.HealthCheck.Path = "/reporting/health"
	p.Sites[0].Machines[0].Services = []blueprint.Service{alarm, reporting}

	require.NoError(t, p.Validate())
}

// TestServicesMayNotShareAProbeEndpoint checks the limit of the rule above. One
// listener answering for two services is a deployment; one endpoint claimed by
// two services is two names for one answer.
func TestServicesMayNotShareAProbeEndpoint(t *testing.T) {
	p := validProject()
	p.Sites[0].Machines[0].Services = []blueprint.Service{
		validService("core-services", 9101),
		validService("alarm-service", 9101),
	}

	require.ErrorContains(t, p.Validate(), "two services may share a port but not an endpoint")
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
