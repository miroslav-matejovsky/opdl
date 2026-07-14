package api

import "net/http"

// Status is the body returned by the platform's status endpoint. It is part of
// the platform's public API contract: the shape SDKs decode and the conformance
// module turns into the OpenAPI response schema. The internal HTTP handler serves
// this exact type, so the served response and the published contract cannot
// drift.
type Status struct {
	// Status is a short, machine-readable state token, e.g. "ok".
	Status string `json:"status"`
	// Message is a human-readable description of the platform state.
	Message string `json:"message"`
}

// Operation is one HTTP operation in the platform's API contract: how it is
// called (Method, Path), what it does (Summary), and the body it returns on
// success.
type Operation struct {
	// Method is the HTTP method, e.g. http.MethodGet.
	Method string
	// Path is the request path the operation is served on, e.g. "/".
	Path string
	// Summary is a one-line human description of the operation.
	Summary string
	// SuccessStatus is the HTTP status code returned on success, e.g. 200.
	SuccessStatus int
	// SuccessBody is a zero value of the success response body type. The
	// conformance module derives the response JSON schema from it by reflection,
	// so the schema always matches the Go type the platform serves.
	SuccessBody any
}

// Contract is the platform's HTTP API surface as the contract sees it: the API
// identity (Title, Version, Description) and the operations it serves.
type Contract struct {
	// Title is the human-readable API name.
	Title string
	// Version is the contract version, independent of the platform binary version.
	Version string
	// Description is a short summary of what the API is.
	Description string
	// Operations are the HTTP operations the platform serves.
	Operations []Operation
}

// Describe returns the platform's API contract. It is the single source the
// conformance module generates the OpenAPI specification from: change the API
// here and regenerate contracts/openapi.yaml.
func Describe() Contract {
	return Contract{
		Title:       "OPDL Platform API",
		Version:     "0.1.0",
		Description: "HTTP API served by the OPDL platform runtime.",
		Operations: []Operation{
			{
				Method:        http.MethodGet,
				Path:          "/",
				Summary:       "Report platform status",
				SuccessStatus: http.StatusOK,
				SuccessBody:   Status{},
			},
		},
	}
}
