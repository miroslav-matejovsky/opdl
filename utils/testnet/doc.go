// Package testnet allocates loopback TCP addresses and ports for tests.
//
// It provides two allocation models for different caller lifecycles:
//
// [Reserve] and [ReserveOn] allocate distinct ephemeral addresses from the
// operating system (such as 127.0.0.1:0) and hold their listeners open until
// explicitly released. Use this model when the caller wants the operating
// system to choose the port and will bind immediately after release. Note that
// between release and bind, there is a race condition where the operating
// system or another process may claim the released port.
//
// [Take] allocates distinct port numbers from a process-wide pool outside the
// operating system ephemeral range (20000 to 32767). It probes each candidate
// port to ensure nothing on the host holds it, then returns the port number
// without holding a listener open. Because returned ports are never reused within
// a process and sit outside the ephemeral range, concurrent tests within the
// process will never collide on them or race with ephemeral allocations. Use
// this model when the caller must publish or compile a port into a configuration
// before binding it.
package testnet
