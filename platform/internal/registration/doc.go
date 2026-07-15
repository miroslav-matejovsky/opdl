// Package registration owns local registration request state and its projection
// into the public API models. It deliberately has no HTTP or deployment loading
// concerns: callers inject the trusted origin location and invoke its service
// with request contexts.
package registration
