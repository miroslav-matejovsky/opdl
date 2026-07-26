// Package config owns the platform's configuration: the deployment descriptor a
// binary is built with, and the effective runtime configuration derived from it.
//
// # One source
//
// A running instance is configured by exactly one thing: the deployment
// descriptor, resolved by the builder from the project blueprint and compiled
// into the binary from deployment.json (see Deployment). It is the machine's
// identity, its instances and their endpoints, and its Primary Ownership lease.
// A binary's descriptor cannot be reassigned at runtime.
//
// There is no configuration file. There used to be a TOML file read from beside
// the executable, holding the settings a site could turn without rebuilding: the
// listener timeouts and the projection lag bound. They are authored in the
// blueprint now, the timeouts on each instance's api block and the lag bound on
// the machine's standby lease, and they are resolved into the descriptor with
// everything else. That removed the one thing two tiers cost: a reader asking
// what an instance is configured with no longer has to know which tier answers,
// and no setting can appear in both and need a precedence rule to tell them
// apart. What a site gives up is turning a timeout without a rebuild, which is
// the same thing it already gives up for every endpoint and every path.
//
// Load parses every duration the descriptor carries and fails fast on a bad one.
// No implicit defaults are applied: a missing or non-positive duration is a
// startup failure rather than a value someone has to guess at later. Deployment
// decodes the descriptor alone, for callers that need the machine's identity
// without parsing anything.
//
// # Per-machine and per-instance
//
// One machine has one descriptor, and its Primary and Standby Instances both
// read it. So nothing that answers for a single instance is held on the machine:
// everything an instance binds or writes on its own — its API address, its data
// directory, the timeouts bounding its listener — is carried on that instance's
// own record inside the descriptor, and the runtime reads its own by role. The
// accessors on Config that take a role take it for that reason, and Summary
// takes it because the role is the only thing that differs between the two
// startup blocks of one machine.
//
// # The descriptor is a contract with the builder
//
// Descriptor and its types mirror the builder's deployment descriptor field for
// field. The platform keeps its own copy rather than importing the builder, so
// the runtime never depends on the build tool; the conformance-tests module
// imports this package alongside the builder's descriptor to verify the two stay
// compatible. That is why this package is exported rather than internal.
//
// The descriptor decodes strictly. UnmarshalJSON requires every resolved
// decision it depends on to be present, because each field it guards has a
// usable zero value: an omitted instances.standby.disabled would decode as false
// and deploy redundancy nobody asked for, and an omitted read header timeout
// would decode as zero, which http.Server reads as no limit at all. The failure
// lands at startup instead of on a machine running with a topology nobody wrote
// down.
package config
