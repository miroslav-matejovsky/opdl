// Package config owns the platform's runtime configuration: the single place
// that turns the platform's deployment descriptor and runtime parameters into
// the configuration the runtime consumes.
//
// In this simplified platform the configuration has two parts: the deployment
// descriptor (see package deployment) that says what the machine is, and a small
// set of platform-owned runtime parameters (currently just the listen port) that
// say how the platform runs. Both are embedded in platform.json as a neutral mock
// so the platform always has a valid configuration to boot with; the builder will
// stage a real per-machine descriptor here once the platform reads one.
//
// Load parses the embedded configuration, applies parameter defaults, and fails
// fast on malformed data. Summary renders the result for logging at startup.
// Typed subsystem configuration will grow here as the platform does.
package config
