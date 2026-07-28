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
// Site composition remains unimplemented. Until site distribution lands, the
// Active state serves the journal-less API surface.
package app
