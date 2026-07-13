// Package deployment is the deployment contract: shapes that define how a single
// machine's platform binary is executed.
//
// Deployment is execution, not intent. A Descriptor is the per-machine artifact
// the builder derives from a project's topology: it names the machine's role,
// the features turned on for it, the service kinds it hosts and their
// parameters, its backing store, and the runtime parameter policy. Those are
// deployment-owned values; together they define a deployment variant. Topology
// (projects, sites, machines, roles, assignments) lives in a separate contract,
// topology, and never reaches here.
//
// # Deployment contract, not a transport
//
// The descriptor is the deployment contract between the builder (producer) and
// the platform (consumer). It is not a transport wire protocol; it is a derived
// deployment artifact both sides understand. The builder marshals a Descriptor
// to JSON and embeds it into a machine's binary; the platform unmarshals it back
// and validates it with Validate. No HCL is parsed at runtime: HCL is an
// authoring format and stays in the blueprint. Package tests verify real JSON
// data fragments against Descriptor to make wire ownership clear and ensure
// JSON struct tags stay accurate.
//
// # Deployment and runtime configuration
//
// A deployment descriptor is not the same as runtime configuration. The
// descriptor carries deployment-owned values, defaults, and an override policy;
// runtime configuration (RuntimeConfig) is provided on the target machine and
// carries machine-specific and operator-managed values. The two overlap by
// design: a parameter may be deployment-only, deployment-defined with a runtime
// override, or runtime-only. The distinction is not the parameter itself but its
// ownership and override policy, declared per parameter in Runtime.
//
// Resolve settles the two inputs into an EffectiveConfig according to that
// policy: deployment defaults apply, permitted overrides from the machine win,
// forbidden overrides and unknown or missing-required parameters are rejected.
// The effective configuration is what the platform actually runs from. Standard
// parameters cover the local HTTP surface, log level, and optional process-level
// cluster membership: a cluster bind address enables gossip membership, a join
// list points at seed members, and an advertise address overrides what peers
// should dial. Separate distributed-data parameters enable an embedded Olric
// member with a Redis-compatible client surface, Olric memberlist address, and
// Olric seed list.
//
// # Node bootstrap parameters
//
// The platform_* parameters are the locally provisioned bootstrap a node needs
// before the site authority and central configuration are reachable: the
// internal node listener bind and advertise addresses, the process slot
// (single/primary/secondary), the ordered authority endpoints, temporary direct
// peer endpoints for the Step 3 fabric, the security mode, and the local
// activation name. They are bootstrap only and carry no
// centrally managed domain values. They ship empty (slot ships "single"), which
// keeps a node standalone with no LAN listener until an operator provisions them.
// The descriptor also records whether the machine hosts the site authority
// (HostsAuthority) and a stable assignment id per hosted service, which is the
// source of that service instance's stable runtime identity. A service assignment
// may also contain static redundancy policy. Single-active group membership,
// lease timing, retry, failover, and drain limits are builder-resolved deployment
// facts. The runtime consumes them to acquire a fenced authority lease; it never
// discovers members or negotiates a group.
//
// # No generated code
//
// These are hand-written types. No Go source is generated for the deployment
// descriptor at any point.
package deployment
