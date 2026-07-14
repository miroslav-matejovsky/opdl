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
	// GeneratedAt is when the package was produced (UTC).
	GeneratedAt time.Time `json:"generated_at"`
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
