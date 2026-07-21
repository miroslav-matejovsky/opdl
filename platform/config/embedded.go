package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// deploymentJSON is the deployment descriptor compiled into this binary.
//
// In a clean checkout it is a neutral, self-contained mock that lets the platform
// build and run standalone with no customer deployment present. That is what
// keeps the repository always compilable without a project selected. The mock
// disables the standby instance because `task run` launches one development
// process; every descriptor states both instance decisions explicitly.
//
// When the builder produces a machine package it overwrites this file with that
// machine's resolved descriptor, runs go build, then restores the mock. The
// platform reads the same embedded file either way. No Go source is generated and
// no HCL is parsed at runtime: customization is data compiled in with Go's embed
// directive, never generated code.
//
//go:embed deployment.json
var deploymentJSON []byte

// Deployment decodes the deployment descriptor baked into this binary. It is the
// descriptor tier on its own, for callers that need the machine's identity
// without a configuration file; Load composes it with the file.
func Deployment() (Descriptor, error) {
	return decodeDescriptor(deploymentJSON)
}

// decodeDescriptor converts descriptor bytes into a Descriptor. It is separate
// from Deployment so a malformed descriptor can be tested without rewriting the
// file that is compiled in.
func decodeDescriptor(data []byte) (Descriptor, error) {
	var d Descriptor
	if err := json.Unmarshal(data, &d); err != nil {
		return Descriptor{}, fmt.Errorf("config: invalid deployment descriptor: %w", err)
	}
	return d, nil
}
