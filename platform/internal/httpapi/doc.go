// Package httpapi wires the platform's registration services into the public
// HTTP API declared in platform/api.
//
// NewHandler binds the platform's huma operations (platform/api.Register) to a
// standard-library ServeMux through the humago adapter, filling the api.Handlers
// seam with closures backed by the command and query services. Operation
// definitions, request decoding, validation, and response encoding all belong to
// huma and platform/api; this package only builds the mux and supplies the domain
// behavior, and never learns which transport carries it.
//
// It knows nothing about how registration is decided, and it must not: it does
// not import the fabric or any backend, it holds no identity of its own, and the
// machine and IP on a response are the platform's, taken from its deployment
// descriptor rather than from a request, a header, or a listener address. A
// status route returning 404 because this machine is not a request's origin is
// the service's answer, not a routing decision made here.
package httpapi
