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

	// ErrCatchUpTimeout reports that a projector did not reach the high-water
	// sequence captured at startup within the configured catch-up bound.
	ErrCatchUpTimeout = errors.New("eventfabric: catch-up timed out")

	// ErrHandlerExhausted reports that a handler's delivery was retried to its
	// configured limit without a successful acknowledgement. The event stays in
	// the journal; the node becomes unready rather than dropping it.
	ErrHandlerExhausted = errors.New("eventfabric: handler delivery exhausted")
)
