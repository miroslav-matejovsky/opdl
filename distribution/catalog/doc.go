// Package catalog is the platform's capability catalog: the vocabulary of
// service kinds it can host and feature keys it can switch on.
//
// It is the single place that answers "what services can the platform host?"
// and "what features can be switched on?". The blueprint validates machine
// assignments against this catalog; the platform registers a factory for each
// service kind and enables behavior per feature key. Because both the model
// side and the runtime side share this one catalog, a service or feature can
// never be named inconsistently across the distribution line.
//
// # Closed to projects, extended by the platform
//
// The catalog is closed to projects: a project selects and configures kinds per
// machine, but never invents a new one. It is open to the platform: a new
// capability (say a radar or camera adapter) is added by writing its runtime
// implementation and recording it here, then shipped as a platform release.
//
// This is deliberately not the "central enum" anti-pattern the distribution
// line exists to avoid. A capability cannot be named here without a matching
// runtime factory: the platform can only host a kind it has code for. So the
// catalog entry always sits next to the behavior it enables and cannot drift
// into a classification-only list edited far from what it classifies. Adding a
// capability is therefore a bounded, two-part platform change (catalog entry
// plus factory), versioned with the contract, not a per-project edit.
package catalog
