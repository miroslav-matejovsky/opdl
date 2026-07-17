// Package app implements the top-level execution and runtime orchestration for
// the platform. It composes the runtime's dependencies — configuration, the
// Event Fabric and its site journal, the node-local registration projection, the
// registration services, and the durable registration handler — and owns their
// lifecycle through startup, execution, and shutdown.
//
// It is the only package that knows the platform coordinates through events on a
// particular transport. Domain packages are handed the narrow roles they need: a
// publisher to state facts, a projector to fold them, a handler to react. The
// HTTP boundary is handed the two registration services. Nothing below this
// package can reach the transport, name a subject, or configure a stream.
//
// Composition also owns what no single component can conclude. Readiness is a
// statement about a whole node — its journal, its projections, and its handlers —
// so this package runs the bounded startup sequence, decides the node is ready,
// and states that into the journal; a transport announcing itself ready would be
// claiming something it cannot know. Shutdown is the same in reverse.
//
// Run is the entry point invoked by the platform command-line interface.
package app
