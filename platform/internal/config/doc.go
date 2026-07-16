// Package config owns the platform's runtime configuration: a small composition
// layer that turns what package embedded provides (the deployment descriptor the
// builder stages before compiling) and the platform's TOML configuration file
// into the Config the runtime consumes.
//
// Two tiers compose here. The embedded deployment descriptor is baked into the
// binary and decoded by package embedded; the platform runs from whatever it
// provides. The TOML configuration file is external, read at startup, and lets a
// user override runtime settings (for now only the API listen address) without
// rebuilding the binary or editing the embedded descriptor. A missing file is not
// an error: the platform falls back to a built-in default address so a clean
// checkout runs standalone.
//
// Load composes a Config from both tiers and fails fast on malformed data.
// Summary renders the result for logging at startup. Typed subsystem
// configuration will grow here as the platform does.
package config
