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
// A machine runs as one or two local processes called slots, and every active-only
// capability this package composes is gated by the machine fence. Run resolves the
// slot from the -instance argument and the machine's warm-standby policy, then
// contends for the fence:
//
//   - The slot that takes the fence runs the active runtime: it opens the storage
//     transport, catches up, attaches the durable handlers, publishes readiness,
//     and serves the public API. It releases the fence only after those resources
//     have closed.
//   - A slot that finds the fence held runs a warm standby: a client-only
//     transport and the projector only, caught up and following the journal, with
//     no handler, no readiness, and no listener. A machine that opted out of warm
//     standby refuses to start a second slot at all.
//
// So two processes on one machine never serve or decide together. Each slot writes
// a local status file for deployment diagnostics, and a projection that lags the
// journal beyond the configured bound stops an active slot serving and marks a
// standby not promotable. The slot, state, fence, status, and ownership contract
// this package builds on is defined in internal/redundancy. Promoting a standby
// into the active runtime after the active exits is later-stage work.
//
// Run is the entry point invoked by the platform command-line interface.
package app
