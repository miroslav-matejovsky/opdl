// Package app composes and runs the platform runtime.
//
// It opens the instance event log, machine event store, state file, embedded
// broker, site clients, service-health monitor, HTTP listener, and ownership
// manager. One process event factory and publisher feed the instance log and
// the scope-filtering machine backend.
//
// Both instance roles keep their own loopback listener bound. Redundancy
// sequences Passive and Active compositions, while app swaps the HTTP handler
// for the current state.
//
// Both Active and Passive instances probe their machine's services and exchange
// expiring observations across the site's NATS cluster. This current-state view
// is separate from durable site-event distribution, which is not implemented.
package app
