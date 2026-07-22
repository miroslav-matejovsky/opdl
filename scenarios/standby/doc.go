// Package standby holds the scenarios about a machine's two fixed instances and
// the ownership that moves between them.
//
// A machine is packaged with a primary launch and, unless it opts out, a standby
// one. Both are described by the package manifest, so these scenarios start the
// processes with the manifest's own arguments rather than arguments they made
// up: what is under test is the launch contract a deployment tool would follow.
//
// ManifestArgumentsMatchRuntime consumes that contract directly.
// WarmStandbyFailoverAndPreferredPrimary drives the full lifecycle over it,
// including repeated failover and failback, and pins the regression that made a
// warm standby derive endpoints the active process was not serving on.
//
// The shared machinery is internal/harness; see its documentation for the
// scratch layout, the machine budget, and the failure diagnostics.
package standby
