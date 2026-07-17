package eventfabric

import "errors"

// These are the typed failures the Event Fabric and its adapters return, so
// runtime composition and domain code can react to a kind of failure without
// depending on a transport's own error types. An adapter wraps its transport's
// error with one of these and with the OPDL operation, route, stream, or
// consumer name for context.
var (
	// ErrClosed reports a call on a fabric that has already been closed.
	ErrClosed = errors.New("eventfabric: fabric is closed")

	// ErrIncompatibleJournal reports an existing site journal whose stored
	// configuration does not match what this build requires. Startup refuses it
	// rather than adopting or mutating a journal it does not understand.
	ErrIncompatibleJournal = errors.New("eventfabric: incompatible site journal")

	// ErrCatchUpTimeout reports that a node did not complete its startup
	// readiness sequence within the configured catch-up bound: a projector that
	// did not reach a captured high-water sequence, or a handler backlog that did
	// not drain.
	ErrCatchUpTimeout = errors.New("eventfabric: catch-up timed out")

	// ErrHandlerExhausted reports that a handler's delivery was retried to its
	// configured limit without a successful acknowledgement. The event stays in
	// the journal; the node becomes unready rather than dropping it.
	ErrHandlerExhausted = errors.New("eventfabric: handler delivery exhausted")

	// ErrHandlerNotAttached reports a query about a handler whose durable
	// consumer does not exist yet. At startup it means the handler's loop has not
	// established it; it is a "not yet", not a failure.
	ErrHandlerNotAttached = errors.New("eventfabric: handler is not attached")
)
