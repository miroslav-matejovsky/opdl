// Package registration owns local registration request state and its projection
// into the public API models. It deliberately has no HTTP or deployment loading
// concerns: callers inject the trusted origin location and invoke its service
// with request contexts.
//
// The package states what it does as domain events, declared and listed in
// events.go and recorded through an injected Recorder at the state transition
// that owns each fact: a first persist is requested, each platform instance's
// acceptance is confirmed, the last expected confirmation is accepted, an
// instance's refusal is rejected, and a refused attempt to reuse a key is
// conflict. Transitions that do not happen produce no event, so an exact retry
// and a validation failure are silent, and the events are usable as evidence of
// behavior rather than of calls. A recording failure surfaces to the caller as
// the operation's error, and the store is not rolled back to match it.
package registration
