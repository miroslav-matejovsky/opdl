// Package config owns the platform's configuration: both tiers of it, and the
// effective configuration they compose into.
//
// # Two tiers
//
// A running instance is configured by two sources, and neither is complete on
// its own:
//
//   - the deployment descriptor, resolved by the builder from the project
//     blueprint and compiled into the binary from deployment.json (see
//     Deployment). It is this machine's identity, its instances and their
//     endpoints, its ownership lock, and its site's membership. A binary's
//     descriptor cannot be reassigned with runtime configuration.
//   - the configuration file, a TOML file read at startup. It is what a site
//     decides for a machine without rebuilding: timeouts, where the journal is
//     stored, where operational events are retained, and the credentials file.
//
// They are one package because the effective configuration is their
// combination, and because a reader asking what an instance is configured with
// should not have to know which tier answers. Descriptor carries the first, the
// accessors on Config carry the second, and Summary renders both as one startup
// block.
//
// The line between them is not a matter of taste. Anything that decides what a
// machine is — identity, endpoints, topology, membership — is the descriptor's,
// so it is fixed when the package is built and is identical every time that
// binary starts. Anything a site may legitimately turn without a rebuild is the
// file's. No setting appears in both, because a setting in both would have two
// sources of truth and would need a precedence rule to tell them apart.
//
// Load composes a Config from both tiers and fails fast on malformed data. No
// implicit defaults are applied: a missing configuration file or an omitted
// required setting is a startup failure rather than a value someone has to guess
// at later. Deployment decodes the descriptor tier alone, for callers that need
// the machine's identity without a configuration file.
//
// # Per-machine and per-instance
//
// One machine has one descriptor and one configuration file, and its Primary and
// Standby Instances both read them. So nothing here answers for a single
// instance: everything an instance binds or writes on its own — its API address,
// its runtime directory, its NATS topology — is carried on that instance's own
// record inside the descriptor, and the runtime reads its own by role. Summary
// takes the running instance's role for the same reason: it is the only thing
// that differs between the two startup blocks of one machine.
//
// # The descriptor is a contract with the builder
//
// Descriptor and its types mirror the builder's deployment descriptor field for
// field. The platform keeps its own copy rather than importing the builder, so
// the runtime never depends on the build tool; the conformance-tests module
// imports this package alongside the builder's descriptor to verify the two stay
// compatible. That is why this package is exported rather than internal.
//
// Both tiers decode strictly, for the same reason in opposite directions.
// UnmarshalJSON requires every resolved decision the descriptor depends on to be
// present, because each field it guards has a usable zero value: an omitted
// instances.standby.disabled would decode as false and deploy redundancy nobody
// asked for. The configuration file rejects a key this schema does not define,
// because a silently dropped setting looks exactly like an applied one. Either
// way, the failure lands at startup instead of on a machine that runs with a
// topology nobody wrote down.
//
// # Secrets
//
// The site's transport credentials are composed here rather than by the
// subsystem that uses them: they are read from their own file, held apart from
// the settings Summary renders, and never logged. A startup block is copied into
// tickets and chat windows, so a secret must not be able to reach it.
package config
