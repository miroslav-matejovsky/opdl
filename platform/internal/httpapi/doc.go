// Package httpapi wires the platform's public HTTP API, declared in
// platform/api, to a standard-library ServeMux.
//
// ServingMode (ModeActive vs ModePassive) explicitly states what surface an
// instance serves. ModeActive serves health and identity for the machine, and
// refuses the domain operations the platform does not have. ModePassive serves
// health and identity for one instance only, refusing all domain operations with
// HTTP 503 and accepting no writes (non-GET/HEAD requests are refused
// structurally).
//
// Both handlers bind the platform's huma operations (platform/api.RegisterInstance
// and platform/api.RegisterHealth) through the humago adapter. Operation
// definitions, request decoding, validation, and response encoding all belong to
// huma and platform/api; this package only builds the mux, and never learns which
// transport carries it.
//
// It holds no identity of its own: the machine and IP on a response are the
// platform's, taken from its deployment descriptor rather than from a request, a
// header, or a listener address.
package httpapi
