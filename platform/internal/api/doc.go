// Package api is the platform's HTTP surface. It builds the handler the runtime
// serves on the configured port.
//
// For now the surface is deliberately minimal: a single endpoint that answers GET
// requests with a JSON document reporting that the platform is running. NewHandler
// wires it into an http.Handler the caller mounts on a server. The package owns
// request handling and response encoding; the caller owns the server lifecycle and
// where the port comes from.
package api
