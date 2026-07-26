// Package storage provides the pluggable event storage backend interface and
// the synchronous fan-out publisher.
//
// A storage Backend accepts stamped envelopes for persistent storage.
// The fan-out publisher stamps a typed event once using an events.Factory,
// then synchronously stores the envelope across all configured backends.
//
// # Ordering and Concurrency
//
// Sequential calls to Publish reach each configured backend in order.
// Concurrent calls to Publish have no defined relative order. Each backend is
// responsible for making its own Store and Close operations safe for concurrent use.
package storage
