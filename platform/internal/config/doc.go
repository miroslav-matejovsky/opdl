// Package config owns the platform's runtime configuration: a small composition
// layer that turns what package embedded provides (the deployment descriptor the
// builder stages before compiling) and the platform's TOML configuration file
// into the Config the runtime consumes.
//
// Two tiers compose here. The embedded deployment descriptor is baked into the
// binary and decoded by package embedded; the platform runs from whatever it
// provides. The TOML configuration file is external, read at startup, and lets a
// user specify common runtime settings and controlled per-instance socket
// overrides without rebuilding the binary. Production endpoints remain
// descriptor facts; an override is an operational fallback, not topology.
// No implicit defaults are applied to required common settings: a missing
// configuration file or omitted required setting yields a fast startup failure.
//
// Load composes a Config from both tiers and fails fast on malformed data.
// Summary renders the result for logging at startup. Typed subsystem
// configuration will grow here as the platform does.
package config
