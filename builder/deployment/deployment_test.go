package deployment_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
)

// The fixture is a machine that deploys both instances, which is the shape the
// rules are about: two independent runtimes on one host, each with its own
// service, local files, and listener.
const (
	machineIP = "10.0.1.10"

	// The api addresses are on loopback. The platform API is machine-local, and
	// that is what the descriptor is checked against.
	primaryAPI        = "127.0.0.1:8080"
	primaryEventsFile = "D:/opdl-data/sensor/primary/events.jsonl"
	primaryStateFile  = "D:/opdl-data/sensor/primary/state.json"
	standbyAPI        = "127.0.0.1:8081"
	standbyEventsFile = "D:/opdl-data/sensor/standby/events.jsonl"
	standbyStateFile  = "D:/opdl-data/sensor/standby/state.json"
)

func validDescriptor() deployment.Descriptor {
	return deployment.Descriptor{
		Platform:       "opdl",
		Project:        "customer-a",
		Environment:    "production",
		Site:           "north",
		Machine:        "sensor",
		MachineProfile: "sensor-node",
		IP:             machineIP,
		Services:       []string{"sensor-services"},
		Primary: deployment.Instance{
			Service:              &deployment.WinService{Name: "sensor-primary", DisplayName: "sensor primary"},
			EventsFile:           primaryEventsFile,
			StateFile:            primaryStateFile,
			APIAddress:           primaryAPI,
			APIReadHeaderTimeout: "5s",
			APIShutdownTimeout:   "10s",
		},
		Standby: &deployment.Instance{
			Service:              &deployment.WinService{Name: "sensor-standby", DisplayName: "sensor standby"},
			EventsFile:           standbyEventsFile,
			StateFile:            standbyStateFile,
			APIAddress:           standbyAPI,
			APIReadHeaderTimeout: "5s",
			APIShutdownTimeout:   "10s",
		},
		Lease: &deployment.Lease{
			File:                  "D:/opdl-data/sensor/lease",
			Duration:              "15s",
			RenewalInterval:       "5s",
			HealthCheckInterval:   "2s",
			FailbackStabilization: "30s",
			LagBound:              "30s",
		},
	}
}

func TestDescriptorValidateOK(t *testing.T) {
	require.NoError(t, validDescriptor().Validate())
}

func TestDescriptorValidateFailures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*deployment.Descriptor)
		errText string
	}{
		{"missing platform", func(d *deployment.Descriptor) { d.Platform = "" }, "platform is required"},
		{"missing project", func(d *deployment.Descriptor) { d.Project = "" }, "project is required"},
		{"missing environment", func(d *deployment.Descriptor) { d.Environment = "" }, "environment is required"},
		{"missing site", func(d *deployment.Descriptor) { d.Site = "" }, "site is required"},
		{"missing machine", func(d *deployment.Descriptor) { d.Machine = "" }, "machine is required"},
		{"missing machine profile", func(d *deployment.Descriptor) { d.MachineProfile = "" }, "machine profile is required"},
		{"invalid ip", func(d *deployment.Descriptor) { d.IP = "not-an-ip" }, "is not a valid IP address"},
		{"no services", func(d *deployment.Descriptor) { d.Services = nil }, "at least one service is required"},
		{"lease set while no standby is deployed", func(d *deployment.Descriptor) { d.Standby = nil }, "omit lease when standby is absent"},
		{"missing lease while a standby is deployed", func(d *deployment.Descriptor) { d.Lease = nil }, "lease is required when a standby is deployed"},

		// Services.
		{"missing primary service", func(d *deployment.Descriptor) { d.Primary.Service = nil }, "primary.service is required"},
		{"missing primary service name", func(d *deployment.Descriptor) { d.Primary.Service.Name = "" }, "primary.service.name is required"},
		{"missing standby service", func(d *deployment.Descriptor) { d.Standby.Service = nil }, "standby.service is required"},
		{"missing standby service name", func(d *deployment.Descriptor) { d.Standby.Service.Name = "" }, "standby.service.name is required"},
		{"colliding service names", func(d *deployment.Descriptor) { d.Standby.Service.Name = d.Primary.Service.Name }, "share service name"},

		// Per-instance endpoints and local files.
		{"missing primary api address", func(d *deployment.Descriptor) { d.Primary.APIAddress = "" }, "primary.api_address is required"},
		{"missing primary events file", func(d *deployment.Descriptor) { d.Primary.EventsFile = "" }, "primary.events_file is required"},
		{"missing primary state file", func(d *deployment.Descriptor) { d.Primary.StateFile = "" }, "primary.state_file is required"},
		{"missing standby api address", func(d *deployment.Descriptor) { d.Standby.APIAddress = "" }, "standby.api_address is required"},
		{"missing standby events file", func(d *deployment.Descriptor) { d.Standby.EventsFile = "" }, "standby.events_file is required"},
		{"missing standby state file", func(d *deployment.Descriptor) { d.Standby.StateFile = "" }, "standby.state_file is required"},
		{
			"one instance points both its files at one path",
			func(d *deployment.Descriptor) { d.Primary.StateFile = d.Primary.EventsFile },
			"primary.events_file and primary.state_file are both",
		},
		{
			"instances share an events file",
			func(d *deployment.Descriptor) { d.Standby.EventsFile = d.Primary.EventsFile },
			"primary.events_file and standby.events_file are both",
		},
		{
			"instances share a state file",
			func(d *deployment.Descriptor) { d.Standby.StateFile = d.Primary.StateFile },
			"primary.state_file and standby.state_file are both",
		},
		{
			// This repo is Windows-only, so two spellings of one path are one file
			// and comparing them literally would let both instances open it.
			"instances share a state file spelled differently",
			func(d *deployment.Descriptor) { d.Standby.StateFile = `D:\OPDL-DATA\sensor\primary\STATE.JSON` },
			"primary.state_file and standby.state_file are both",
		},
		{
			"api address off loopback",
			func(d *deployment.Descriptor) { d.Primary.APIAddress = "10.0.1.10:8080" },
			"is not on the loopback interface",
		},
		{
			"missing primary read header timeout",
			func(d *deployment.Descriptor) { d.Primary.APIReadHeaderTimeout = "" },
			"primary.api_read_header_timeout is required",
		},
		{
			"non-positive primary read header timeout",
			func(d *deployment.Descriptor) { d.Primary.APIReadHeaderTimeout = "0s" },
			"must be positive",
		},
		{
			"missing standby shutdown timeout",
			func(d *deployment.Descriptor) { d.Standby.APIShutdownTimeout = "" },
			"standby.api_shutdown_timeout is required",
		},
		{
			"missing lease lag bound",
			func(d *deployment.Descriptor) { d.Lease.LagBound = "" },
			"lease.lag_bound is required",
		},
		{
			"instances share an api address",
			func(d *deployment.Descriptor) { d.Standby.APIAddress = d.Primary.APIAddress },
			"cannot share a listener",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := validDescriptor()
			tc.mutate(&d)
			require.ErrorContains(t, d.Validate(), tc.errText)
		})
	}
}

// TestDescriptorValidateAcceptsOneInstanceMachine checks a machine that deploys
// no standby is a valid deployment: one instance that binds its API, no peer,
// and no lease to contend for.
func TestDescriptorValidateAcceptsOneInstanceMachine(t *testing.T) {
	d := validDescriptor()
	d.Standby = nil
	d.Lease = nil
	require.NoError(t, d.Validate())
}
