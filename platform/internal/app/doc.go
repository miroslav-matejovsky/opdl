// Package app implements the top-level execution and runtime orchestration for
// the platform. It composes the runtime dependencies — configuration, event log,
// fabric member, and registration service — and manages their lifecycle during
// process startup, execution, and graceful shutdown.
//
// Run is the entry point invoked by the platform command-line interface.
package app
