// Package httpapi is the platform's HTTP handler: the server-side implementation
// of the API contract declared in platform/api. It builds the handler the runtime
// serves on the configured port.
//
// NewHandler wires registration service dependencies into exact request, status,
// and list routes. The package owns strict request decoding and response encoding;
// the caller owns server lifecycle and the trusted descriptor location injected
// into the registration service. Response body types come from platform/api so
// the served JSON and published contract stay identical.
package httpapi
