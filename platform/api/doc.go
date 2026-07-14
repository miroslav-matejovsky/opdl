// Package api is the platform's public HTTP API contract: the response types the
// runtime serves and a structural description of the operations it exposes.
//
// It holds no server code. The internal HTTP handler
// (platform/internal/httpapi) implements the contract described here, and the
// conformance module renders Describe into the OpenAPI specification checked into
// api-specifications/. Keeping the contract in one exported package gives SDKs and the
// conformance module a single, dependency-free source to build against without
// reaching into the platform's internals.
package api
