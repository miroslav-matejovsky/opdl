// Package nats holds the scenarios about the Event Fabric underneath a site:
// which machines store the site journal, who routes to whom, and what survives
// losing one of them.
//
// Nothing here configures a topology. Storage is selected per platform instance
// by sorted name, so every machine derives its own part from the same blueprint
// the harness rendered and built. That derivation is exactly what these
// scenarios read back out of the machines' own startup output.
//
// TwoMachineEventFabric is the smallest deployment that has to form a real
// fabric, and pins the startup order: listener, then fabric, then activation.
// FourMachineStorageTopologyAndFailure proves both halves of the three-storage
// rule, then removes a storage machine and requires the site to keep accepting
// and projecting.
//
// The shared machinery is internal/harness; see its documentation for the
// scratch layout, the machine budget, and the failure diagnostics.
package nats
