// Package state persists one instance's epoch.
//
// The epoch advances before process start and activation, and is written
// atomically before being returned. Separate counters record why it advanced.
// Stepping down does not advance it. An invalid existing file is an error rather
// than a reason to reset the counter.
//
// Primary and standby instances have independent state files and epochs. See
// internal/instance/README.md for the lifecycle model.
package state
