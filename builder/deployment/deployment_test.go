package deployment_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
)

func validDescriptor() deployment.Descriptor {
	return deployment.Descriptor{
		Platform:    "opdl",
		Project:     "customer-a",
		Environment: "production",
		Site:        "north",
		Machine:     "sensor",
		Role:        "sensor-node",
		IP:          "10.0.1.10",
		Services:    []string{"sensor-services"},
		Features:    deployment.Features{Chaos: true},
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
		{"missing role", func(d *deployment.Descriptor) { d.Role = "" }, "role is required"},
		{"invalid ip", func(d *deployment.Descriptor) { d.IP = "not-an-ip" }, "not a valid IP address"},
		{"no services", func(d *deployment.Descriptor) { d.Services = nil }, "at least one service is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := validDescriptor()
			tc.mutate(&d)
			require.ErrorContains(t, d.Validate(), tc.errText)
		})
	}
}
