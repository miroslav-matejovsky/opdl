// Package config owns the platform's runtime configuration: a small composition
// layer that turns what package embedded provides (the deployment descriptor the
// builder stages before compiling) and the platform's TOML configuration file
// into the Config the runtime consumes.
//
// Two tiers compose here. The embedded deployment descriptor is baked into the
// binary and decoded by package embedded; the platform runs from whatever it
// provides. The TOML configuration file is external, read at startup, and lets a
// user specify runtime settings (such as listen address, timeouts, and reconcile
// intervals) without rebuilding the binary or editing the embedded descriptor.
// No implicit defaults are applied: a missing configuration file or omitted
// required settings yield a fast startup failure.
//
// Load composes a Config from both tiers and fails fast on malformed data.
// Summary renders the result for logging at startup. Typed subsystem
// configuration will grow here as the platform does.
package config
