package pack

import "time"

// Manifest and Release are the metadata files that ship inside a deployment
// package. They live here because pack is the only thing that produces them: the
// Manifest describes what the package is and how to deploy it, the Release
// describes how the package was produced. Together they make a package
// self-describing and auditable on the customer premises without reference to
// the build environment.

// Manifest is the deployment manifest shipped in a package.
type Manifest struct {
	// Project, Site, Machine, Role are the package's place in the topology.
	Project string `json:"project"`
	Site    string `json:"site"`
	Machine string `json:"machine"`
	Role    string `json:"role"`
	// Platform identifies the product line the binary is from.
	Platform string `json:"platform"`
	// Binary is the runtime binary filename in the package.
	Binary string `json:"binary"`
	// Services are the service groups this machine hosts.
	Services []string `json:"services"`
	// Deployment is the deployment descriptor filename in the package.
	Deployment string `json:"deployment"`
	// Instances are the independent processes (slots) a deployer must launch for
	// this machine, in start order. Every slot runs the same Binary with the same
	// Deployment descriptor and differs only by its Args. A machine with warm
	// standby lists two slots; a machine that opted out lists one.
	Instances []Instance `json:"instances"`
	// StopOrder lists the slots by name in the order a full machine shutdown stops
	// them: the standby candidate before the active candidate. A service manager
	// may start the slots in either order but must stop them in this one, so a
	// whole-machine shutdown does not look like an active failure that the
	// surviving slot should promote into.
	StopOrder []string `json:"stop_order"`
	// GeneratedAt is when the package was produced (UTC).
	GeneratedAt time.Time `json:"generated_at"`
}

// Instance is one launchable process (slot) of a machine's deployment. Both slots
// of a machine share one Binary and one Deployment descriptor; only the Args that
// select the local slot differ.
type Instance struct {
	// Slot is the stable local process identity, "a" or "b".
	Slot string `json:"slot"`
	// Args are the command-line arguments that select this slot, added to the
	// binary's normal arguments, e.g. ["-instance", "a"].
	Args []string `json:"args"`
}

// slotFlag is the platform binary argument that selects a local slot.
const slotFlag = "-instance"

const (
	slotA = "a"
	slotB = "b"
)

// launchInstances derives the processes a deployer must launch for a machine and
// the order a full machine shutdown stops them, from the machine's warm-standby
// policy.
//
// A machine with warm standby runs two slots, a and b, each the same binary and
// descriptor selected by a different -instance argument. A machine that opted out
// runs only slot a. Stop order is the standby candidate (b) before the active
// candidate (a): a full shutdown stopping the active first would look like a
// failure the surviving slot should promote into.
func launchInstances(warmStandby bool) (instances []Instance, stopOrder []string) {
	instances = []Instance{{Slot: slotA, Args: []string{slotFlag, slotA}}}
	stopOrder = []string{slotA}
	if warmStandby {
		instances = append(instances, Instance{Slot: slotB, Args: []string{slotFlag, slotB}})
		stopOrder = []string{slotB, slotA}
	}
	return instances, stopOrder
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
