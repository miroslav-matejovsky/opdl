// Package deployment is the deployment contract: shapes that define how a
// single machine's platform binary is executed.
//
// Deployment is execution, not intent. A Descriptor is the per-machine
// artifact the builder derives from a project's topology: it names the
// machine's identity within its project, the features turned on for it, and
// the service groups it hosts. Those are deployment-owned values; together
// they define a deployment variant.
//
// # Deployment contract, not a transport
//
// The descriptor is the deployment contract between the builder (producer)
// and the platform (consumer). It is not a transport wire protocol; it is a
// derived deployment artifact both sides understand. The builder marshals a
// Descriptor to JSON and embeds it into a machine's binary; the platform
// unmarshals it back and validates it with Validate. No HCL is parsed at
// runtime: HCL is an authoring format and stays in the blueprint. Package
// tests verify real JSON data fragments against Descriptor to make wire
// ownership clear and ensure JSON struct tags stay accurate.
//
// # No generated code
//
// Descriptor is a hand-written type. No Go source is generated for the
// deployment descriptor at any point.
package deployment
