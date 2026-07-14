// Package embedded holds the deployment descriptor baked into the platform
// binary at build time, and owns decoding it.
//
// deployment.json is the embedded deployment descriptor. In a clean checkout it
// is a neutral, self-contained mock that lets the platform build and run
// standalone with no customer deployment present. This is what keeps the
// repository always compilable without any project selected.
//
// When the builder produces a machine package it overwrites deployment.json
// with that machine's resolved descriptor (from the project blueprint), runs go
// build, then restores the mock. The platform reads the same embedded file
// either way: Deployment decodes the JSON at startup and the caller runs from
// it.
//
// No Go source is generated here, and no HCL is parsed at runtime. Customization
// is data (JSON) compiled in with Go's embed directive, never generated code.
package embedded

import _ "embed"

// DeploymentJSON is the embedded deployment descriptor compiled into this
// binary. Prefer Deployment, which decodes it; DeploymentJSON is exported for
// callers that need the raw bytes, such as tests.
//
//go:embed deployment.json
var DeploymentJSON []byte
