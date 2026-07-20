// Package api is the platform's public HTTP API: the registration request and
// response types the runtime serves, and the huma operations that serve them.
//
// It owns the contract end to end. Register attaches every operation to a huma
// API; NewServeMux builds the http.Handler the runtime serves; OpenAPIYAML
// renders the OpenAPI 3.0.3 document the conformance-tests module writes to
// api-specifications/ and the .NET SDK is generated from. The same Register call
// serves requests and generates the specification, so the served contract and
// the published document cannot drift.
//
// The package depends on huma but not on the platform's internals. Operations
// reach the domain through Handlers, a struct of function fields the runtime
// fills from the registration services, so this package never imports
// registration (which imports this one) and stays free of the event transport.
package api
