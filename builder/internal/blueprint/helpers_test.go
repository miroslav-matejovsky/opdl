package blueprint_test

import (
	"testing"

	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/internal/blueprint"
)

type topologyFile struct {
	Projects []blueprint.Project `hcl:"project,block"`
}

func decodeHCL(t *testing.T, src string) blueprint.Project {
	t.Helper()
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL([]byte(src), "test.hcl")
	require.False(t, diags.HasErrors(), diags.Error())

	var tf topologyFile
	diags = gohcl.DecodeBody(file.Body, nil, &tf)
	require.False(t, diags.HasErrors(), diags.Error())
	require.Len(t, tf.Projects, 1)
	return tf.Projects[0]
}

func validProject() *blueprint.Project {
	return &blueprint.Project{
		Name:        "customer-a",
		Environment: "production",
		Sites: []blueprint.Site{{
			Name:     "north",
			Machines: []blueprint.Machine{validMachine(), namedMachine("relay", "10.0.1.11")},
		}},
	}
}

func validMachine() blueprint.Machine {
	return blueprint.Machine{
		Name:           "sensor",
		MachineProfile: "sensor-node",
		IP:             "10.0.1.10",
		Services:       []string{"sensor-services"},
		EventstoreFile: "D:/opdl/sensor/machine-events.jsonl",
		Primary: &blueprint.Primary{
			EventlogFile: "D:/opdl/sensor/primary/events.jsonl",
			StateFile:    "D:/opdl/sensor/primary/state.json",
			LogFile:      "D:/opdl/sensor/primary/platform.log",
			API:          &blueprint.API{LocalPort: 8080, ReadHeaderTimeout: "5s", ShutdownTimeout: "10s"},
			WinService:   &blueprint.WinService{Name: "primary"},
		},
		Standby: &blueprint.Standby{
			EventlogFile: "D:/opdl/sensor/standby/events.jsonl",
			StateFile:    "D:/opdl/sensor/standby/state.json",
			LogFile:      "D:/opdl/sensor/standby/platform.log",
			Lease:        validLease("D:/opdl/sensor/lease"),
			API:          &blueprint.API{LocalPort: 8081, ReadHeaderTimeout: "5s", ShutdownTimeout: "10s"},
			WinService:   &blueprint.WinService{Name: "standby"},
		},
	}
}

func namedMachine(name, ip string) blueprint.Machine {
	m := validMachine()
	m.Name = name
	m.IP = ip
	m.Primary.WinService = &blueprint.WinService{Name: name + "-primary"}
	m.EventstoreFile = "D:/opdl/" + name + "/machine-events.jsonl"
	m.Primary.EventlogFile = "D:/opdl/" + name + "/primary/events.jsonl"
	m.Primary.StateFile = "D:/opdl/" + name + "/primary/state.json"
	m.Primary.LogFile = "D:/opdl/" + name + "/primary/platform.log"
	m.Standby.WinService = &blueprint.WinService{Name: name + "-standby"}
	m.Standby.EventlogFile = "D:/opdl/" + name + "/standby/events.jsonl"
	m.Standby.StateFile = "D:/opdl/" + name + "/standby/state.json"
	m.Standby.LogFile = "D:/opdl/" + name + "/standby/platform.log"
	m.Standby.Lease = validLease("D:/opdl/" + name + "/lease")
	return m
}

func validLease(file string) *blueprint.Lease {
	return &blueprint.Lease{
		File:                  file,
		Duration:              "15s",
		RenewalInterval:       "5s",
		HealthCheckInterval:   "2s",
		FailbackStabilization: "30s",
		LagBound:              "30s",
	}
}

func disableStandby(p *blueprint.Project) {
	standby := p.Sites[0].Machines[0].Standby
	standby.Disabled = true
	standby.EventlogFile = ""
	standby.StateFile = ""
	standby.LogFile = ""
	standby.Lease = nil
	standby.API = nil
	standby.WinService = nil
}
