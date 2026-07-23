// Package smoke holds the scenario that proves the platform is deployable at
// all.
//
// It is the suite's floor: the smallest deployment the builder will produce and
// the runtime will start, driven from outside through the packaged binary the
// builder just compiled. One machine, one Primary Instance, no standby. If this
// scenario fails, nothing larger is worth running, because the failure is in
// building or booting rather than in anything a bigger topology would be about.
//
// The shared machinery is internal/harness; see its documentation for the
// scratch layout, the machine budget, and the failure diagnostics.
package smoke
