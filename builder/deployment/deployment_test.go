package deployment_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
)

// The fixture is a three-machine site where this machine deploys both instances
// and its peers deploy one each. That is the shape the rules are about: four
// storage servers on three machines, two of them sharing a host.
const (
	machineIP   = "10.0.1.10"
	gatewayIP   = "10.0.1.11"
	historianIP = "10.0.1.12"

	// The api addresses are on loopback; every Event Fabric address is on the
	// machine ip. That split is what the descriptor is checked against.
	primaryAPI     = "127.0.0.1:8080"
	primaryDataDir = "D:/opdl-data/sensor/primary"
	standbyAPI     = "127.0.0.1:8081"
	standbyDataDir = "D:/opdl-data/sensor/standby"
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
		Instances: deployment.Instances{
			Primary: deployment.Instance{
				Disabled:             false,
				Service:              &deployment.WinService{Name: "sensor-primary", DisplayName: "sensor primary"},
				DataDir:              primaryDataDir,
				APIAddress:           primaryAPI,
				APIReadHeaderTimeout: "5s",
				APIShutdownTimeout:   "10s",
			},
			Standby: deployment.Instance{
				Disabled:             false,
				Service:              &deployment.WinService{Name: "sensor-standby", DisplayName: "sensor standby"},
				DataDir:              standbyDataDir,
				APIAddress:           standbyAPI,
				APIReadHeaderTimeout: "5s",
				APIShutdownTimeout:   "10s",
			},
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
		{"lease set while standby disabled", func(d *deployment.Descriptor) { d.Instances.Standby.Disabled = true }, "omit lease when no standby is deployed"},
		{"primary instance disabled", func(d *deployment.Descriptor) { d.Instances.Primary.Disabled = true }, "instances.primary.disabled: a machine must deploy a Primary Instance"},

		// Services.
		{"missing primary service", func(d *deployment.Descriptor) { d.Instances.Primary.Service = nil }, "instances.primary.service is required"},
		{"missing primary service name", func(d *deployment.Descriptor) { d.Instances.Primary.Service.Name = "" }, "instances.primary.service.name is required"},
		{"missing standby service", func(d *deployment.Descriptor) { d.Instances.Standby.Service = nil }, "instances.standby.service is required"},
		{"missing standby service name", func(d *deployment.Descriptor) { d.Instances.Standby.Service.Name = "" }, "instances.standby.service.name is required"},
		{"colliding service names", func(d *deployment.Descriptor) { d.Instances.Standby.Service.Name = d.Instances.Primary.Service.Name }, "share service name"},

		// Per-instance endpoints and data directories.
		{"missing primary api address", func(d *deployment.Descriptor) { d.Instances.Primary.APIAddress = "" }, "instances.primary.api_address is required"},
		{"missing primary data dir", func(d *deployment.Descriptor) { d.Instances.Primary.DataDir = "" }, "instances.primary.data_dir is required"},
		{"missing standby api address", func(d *deployment.Descriptor) { d.Instances.Standby.APIAddress = "" }, "instances.standby.api_address is required"},
		{"missing standby data dir", func(d *deployment.Descriptor) { d.Instances.Standby.DataDir = "" }, "instances.standby.data_dir is required"},
		{
			"instances share a platform data dir",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.DataDir = d.Instances.Primary.DataDir
			},
			"cannot share a platform data directory",
		},
		{
			"api address off loopback",
			func(d *deployment.Descriptor) { d.Instances.Primary.APIAddress = "10.0.1.10:8080" },
			"is not on the loopback interface",
		},
		{
			"missing primary read header timeout",
			func(d *deployment.Descriptor) { d.Instances.Primary.APIReadHeaderTimeout = "" },
			"instances.primary.api_read_header_timeout is required",
		},
		{
			"non-positive primary read header timeout",
			func(d *deployment.Descriptor) { d.Instances.Primary.APIReadHeaderTimeout = "0s" },
			"must be positive",
		},
		{
			"missing standby shutdown timeout",
			func(d *deployment.Descriptor) { d.Instances.Standby.APIShutdownTimeout = "" },
			"instances.standby.api_shutdown_timeout is required",
		},
		{
			"missing lease lag bound",
			func(d *deployment.Descriptor) { d.Lease.LagBound = "" },
			"lease.lag_bound is required",
		},
		{
			"instances share an api address",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.APIAddress = d.Instances.Primary.APIAddress
			},
			"cannot share a listener",
		},

		// A standby that is not deployed carries nothing it would have bound.
		{
			"standby service while disabled",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.Disabled = true
				d.Lease = nil
				d.Instances.Standby.APIAddress = ""
			},
			"instances.standby.service is set but the standby is disabled",
		},
		{
			"standby api address while disabled",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.Disabled = true
				d.Lease = nil
				d.Instances.Standby.Service = nil
			},
			"instances.standby.api_address is set but the standby is disabled",
		},
		{
			"standby api timeouts while disabled",
			func(d *deployment.Descriptor) {
				d.Instances.Standby.Disabled = true
				d.Lease = nil
				d.Instances.Standby.Service = nil
				d.Instances.Standby.APIAddress = ""
				d.Instances.Standby.DataDir = ""
			},
			"instances.standby api timeouts are set but the standby is disabled",
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
// no standby is a valid deployment.
func TestDescriptorValidateAcceptsOneInstanceMachine(t *testing.T) {
	d := validDescriptor()
	d.Instances.Standby = deployment.Instance{Disabled: true}
	d.Lease = nil
	require.NoError(t, d.Validate())
}

// TestDescriptorValidateAcceptsOneMemberSite checks a single-machine,
// single-instance site is a valid deployment.
func TestDescriptorValidateAcceptsOneMemberSite(t *testing.T) {
	d := validDescriptor()
	d.Instances.Standby = deployment.Instance{Disabled: true}
	d.Lease = nil
	require.NoError(t, d.Validate())
}

// TestDescriptorValidateAcceptsAMachineWithNoEventStorage checks the descriptor
// a machine that authored no event storage resolves to: one instance that binds
// its API and nothing else.
func TestDescriptorValidateAcceptsAMachineWithNoEventStorage(t *testing.T) {
	d := validDescriptor()
	d.Instances.Standby = deployment.Instance{Disabled: true}
	d.Lease = nil
	require.NoError(t, d.Validate())
}
