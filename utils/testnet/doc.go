// Package testnet reserves distinct ephemeral loopback TCP addresses for tests.
//
// It accepts a context and count and returns a reservation that holds every
// listener open until explicitly released. Holding all listeners open until
// reservation completes ensures that every allocated address is distinct.
//
// Unavoidable race condition: once [Reservation.Release] closes the listeners,
// the operating system may assign a released port to another process before the
// consumer binds it. This package guarantees distinct selection while reserved,
// not exclusive ownership after release. Callers that can accept an already-open
// [net.Listener] should keep using a listener directly rather than reserving and
// releasing addresses.
package testnet
