// Package config owns the platform's runtime configuration: a small
// composition layer that turns what package embedded provides (the deployment
// descriptor the builder stages before compiling) into the Config the runtime
// consumes.
//
// Load composes a Config from the platform's embedded configuration and fails
// fast on malformed data. Summary renders the result for logging at startup.
// There is no platform-owned configuration tier or hardcoded default here:
// decoding is package embedded's job, and everything the platform runs with
// comes from what it provides. Typed subsystem configuration will grow here as
// the platform does.
package config
