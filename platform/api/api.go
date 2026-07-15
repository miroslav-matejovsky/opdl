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

// PathParameter describes one parameter embedded in an operation path. Type is
// a zero value of the Go type used to parse it; the conformance module derives
// its OpenAPI schema and numeric bounds from that type.
type PathParameter struct {
	// Name is the path placeholder name, without braces.
	Name string
	// Type is a zero value of the parameter's Go type.
	Type any
	// Required reports whether clients must provide this parameter.
	Required bool
}

// RequestBody describes an operation's JSON request body. Type is a zero value
// of the Go type decoded by the platform.
type RequestBody struct {
	// Type is a zero value of the request body's Go type.
	Type any
	// Required reports whether clients must provide a request body.
	Required bool
}

// Response describes one documented HTTP response. Body is a zero value of its
// JSON body type; nil means the response has no JSON body.
type Response struct {
	// Status is the HTTP status code, e.g. http.StatusOK.
	Status int
	// Body is a zero value of the response body type, or nil when absent.
	Body any
}

// Operation is one HTTP operation in the platform's API contract: how it is
// called, what it does, and its inputs and documented responses.
type Operation struct {
	// Method is the HTTP method, e.g. http.MethodGet.
	Method string
	// Path is the request path the operation is served on, e.g. "/".
	Path string
	// OperationID is the stable OpenAPI operationId used by generated SDKs.
	OperationID string
	// Summary is a one-line human description of the operation.
	Summary string
	// PathParameters are the values embedded in Path.
	PathParameters []PathParameter
	// RequestBody is the optional JSON body accepted by the operation.
	RequestBody *RequestBody
	// Responses are the documented HTTP responses returned by the operation.
	Responses []Response
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
// here and regenerate api-specifications/openapi.yaml.
func Describe() Contract {
	return Contract{
		Title:       "OPDL Platform API",
		Version:     "0.1.0",
		Description: "HTTP API served by the OPDL platform runtime.",
		Operations: []Operation{
			{
				Method:      http.MethodGet,
				Path:        "/",
				OperationID: "getPlatformStatus",
				Summary:     "Report platform status",
				Responses: []Response{
					{Status: http.StatusOK, Body: Status{}},
				},
			},
		},
	}
}
