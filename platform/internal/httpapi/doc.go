// Package httpapi is the platform's HTTP handler: the server-side implementation
// of the API contract declared in platform/api. It builds the handler the runtime
// serves on the configured port.
//
// NewHandler wires the registration service into exact request, status, list,
// and conflict routes. The package owns strict request decoding and response
// encoding; the caller owns the server's lifecycle. Response body types come
// from platform/api so the served JSON and the published contract stay
// identical.
//
// It knows nothing about how registration is decided, and it must not: it does
// not import the fabric or any backend, it holds no identity of its own, and the
// machine and IP on a response are the platform's, taken from its deployment
// descriptor rather than from a request, a header, or a listener address. A
// status route returning 404 because this machine is not a request's origin is
// the service's answer, not a routing decision made here.
package httpapi
