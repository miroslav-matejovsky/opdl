// Package app composes and runs the platform runtime.
//
// It opens the instance event log, machine event store, state file, HTTP
// listener, and ownership manager. One process event factory and publisher feed
// the instance log and the scope-filtering machine backend.
//
// Both instance roles keep their own loopback listener bound. Redundancy
// sequences Passive and Active compositions, while app swaps the HTTP handler
// for the current state.
//
// The platform distributes no site state. An Active instance serves health and
// identity for its machine, and a Passive one waits for ownership with nothing
// to catch up on.
package app
