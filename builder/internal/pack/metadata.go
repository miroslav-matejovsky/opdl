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
	// Slots are the independent processes a deployer must launch for this machine,
	// in start order. Every slot runs the same Binary with the same Deployment
	// descriptor and differs only by its Args. A machine with warm standby lists
	// two slots; a machine that opted out lists one.
	Slots []SlotLaunch `json:"slots"`
	// ShutdownStrategy tells deployment tooling how to stop the machine. A
	// redundant machine must inspect live slot status and stop the current standby
	// before the current active; symmetric slots cannot have a safe static order.
	ShutdownStrategy string `json:"shutdown_strategy"`
	// GeneratedAt is when the package was produced (UTC).
	GeneratedAt time.Time `json:"generated_at"`
}

// SlotLaunch is one launchable process slot of a machine's deployment. Both
// slots share one Binary and one Deployment descriptor; only the Args that
// select the local slot differ.
type SlotLaunch struct {
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

	shutdownSingleSlot        = "single_slot"
	shutdownStandbyThenActive = "standby_then_active"
)

// launchSlots derives the processes a deployer must launch for a machine and its
// full-machine shutdown strategy from the warm-standby policy.
//
// A machine with warm standby runs two slots, a and b, each the same binary and
// descriptor selected by a different -instance argument. A machine that opted out
// runs only slot a. Redundant slots are symmetric, so shutdown tooling reads
// their live status and stops whichever slot is standby before the active.
func launchSlots(warmStandby bool) (slots []SlotLaunch, shutdownStrategy string) {
	slots = []SlotLaunch{{Slot: slotA, Args: []string{slotFlag, slotA}}}
	shutdownStrategy = shutdownSingleSlot
	if warmStandby {
		slots = append(slots, SlotLaunch{Slot: slotB, Args: []string{slotFlag, slotB}})
		shutdownStrategy = shutdownStandbyThenActive
	}
	return slots, shutdownStrategy
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
