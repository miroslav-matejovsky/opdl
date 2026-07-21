package pack

import (
	"time"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
)

// Manifest and Release are the metadata files that ship inside a deployment
// package. They live here because pack is the only thing that produces them: the
// Manifest describes what the package is and how to deploy it, the Release
// describes how the package was produced. Together they make a package
// self-describing and auditable on the customer premises without reference to
// the build environment.

// Manifest is the deployment manifest shipped in a package.
type Manifest struct {
	// Project, Site, Machine, MachineRole are the package's place in the topology.
	//
	// MachineRole is the machine's purpose, such as "sensor-node". It is
	// deliberately not called role: the two launches below carry the other kind of
	// role, the instance role, and one word for both axes reads as one decision.
	Project     string `json:"project"`
	Site        string `json:"site"`
	Machine     string `json:"machine"`
	MachineRole string `json:"machine_role"`
	// Platform identifies the product line the binary is from.
	Platform string `json:"platform"`
	// Binary is the runtime binary filename in the package.
	Binary string `json:"binary"`
	// Services are the service groups this machine hosts.
	Services []string `json:"services"`
	// Deployment is the deployment descriptor filename in the package.
	Deployment string `json:"deployment"`
	// Primary is the Primary Instance, always deployed.
	Primary Launch `json:"primary"`
	// Standby is the Standby Instance, deployed only when the machine's blueprint
	// enables it.
	Standby *Launch `json:"standby,omitempty"`
	// GeneratedAt is when the package was produced (UTC).
	GeneratedAt time.Time `json:"generated_at"`
}

// Launch is one instance's invocation from the deployment manifest: what to call
// the service that runs it, and how to invoke it.
type Launch struct {
	// Service is the instance's Windows Service identity. The platform installs
	// and manages nothing; this states what whoever installs the service should
	// call it, so the two fixed roles are named identically on every machine.
	Service WinService `json:"service"`
	// Args are added to the binary's normal arguments.
	Args []string `json:"args"`
}

// WinService is one instance's Windows Service identity as shipped in a manifest.
type WinService struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
}

// instanceFlag selects an instance role.
const instanceFlag = "-instance"

const (
	primaryInstance = "primary"
	standbyInstance = "standby"
)

// launches builds one launch record per deployed instance. The descriptor's slots
// already state which instances are deployed and what their services are called,
// so the manifest restates that in the form an installer needs rather than
// deciding anything.
func launches(slots deployment.Slots) (primary Launch, standby *Launch) {
	primary = Launch{
		Service: winService(slots.Primary.Service),
		Args:    []string{instanceFlag, primaryInstance},
	}
	if slots.Standby.Disabled {
		return primary, nil
	}
	standby = &Launch{
		Service: winService(slots.Standby.Service),
		Args:    []string{instanceFlag, standbyInstance},
	}
	return primary, standby
}

func winService(service *deployment.WinService) WinService {
	if service == nil {
		return WinService{}
	}
	return WinService{Name: service.Name, DisplayName: service.DisplayName, Description: service.Description}
}

// Release is the release metadata shipped in a package.
type Release struct {
	// Builder identifies the tool that produced the package.
	Builder string `json:"builder"`
	// BuiltAt is when the package was built (UTC).
	BuiltAt time.Time `json:"built_at"`
	// OS and Arch are the compile target.
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// BinarySHA256 is the hex-encoded SHA-256 of the binary.
	BinarySHA256 string `json:"binary_sha256"`
}
