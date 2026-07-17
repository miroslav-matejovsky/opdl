package api

import "net/http"

const (
	// RoleMaster identifies the master role a registered unit may advertise.
	RoleMaster = "Master"
	// RoleSlave identifies the slave role a registered unit may advertise.
	RoleSlave = "Slave"

	// RegistrationStatusPending means a registration waits for confirmation.
	RegistrationStatusPending = "pending"
	// RegistrationStatusAccepted means every expected platform instance accepted it.
	RegistrationStatusAccepted = "accepted"
	// RegistrationStatusRejected means at least one platform instance rejected it.
	RegistrationStatusRejected = "rejected"

	// RegistrationConflictResolutionResolved means deterministic contender
	// selection identified the proposal that survives a duplicate claim.
	RegistrationConflictResolutionResolved = "resolved"
)

// RegistrationRequest is the client-supplied request to register one unit.
// Machine, IP, registration status, and platform-instance progress are supplied
// only by the platform in Registration responses.
//
// Registration is asynchronous. A valid request is durably recorded and answered
// with a ProposalAccepted; whether the site registers the unit is decided
// afterwards and read back through the proposal's status.
type RegistrationRequest struct {
	// UnitType is the unit type identifier from 0 through 255.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the unit identifier from 0 through 65535.
	UnitID uint16 `json:"unit_id"`
	// UnitTypeNameAdvertised is the required non-blank advertised unit type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the optional Master or Slave role advertised by the unit.
	Role *string `json:"role,omitempty"`
}

// ProposalAccepted acknowledges that the site journal durably recorded a
// registration proposal. It is not an acceptance of the registration: it means
// only that the proposal is retained and that the site will decide it.
//
// ProposalID is the client's handle on that decision, and it is deterministic:
// an identical request always produces the same proposal, so a retry returns the
// same ID and adds nothing to the journal.
type ProposalAccepted struct {
	// ProposalID is the proposal's stable identity and its status key.
	ProposalID string `json:"proposal_id"`
	// Sequence is the proposal's position in the site journal.
	Sequence uint64 `json:"sequence"`
}

// Registration is the platform's immutable view of one registration request.
type Registration struct {
	// ProposalID is the proposal's stable identity and its status key.
	ProposalID string `json:"proposal_id"`
	// UnitType is the registered unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the registered unit identifier.
	UnitID uint16 `json:"unit_id"`
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the optional Master or Slave role advertised by the unit.
	Role *string `json:"role,omitempty"`
	// Machine is the descriptor machine where the request originated.
	Machine string `json:"machine"`
	// IP is the descriptor IP where the request originated.
	IP string `json:"ip"`
	// Status is pending, accepted, or rejected.
	Status string `json:"status"`
	// Reason is an optional bounded machine-readable rejection code.
	Reason *string `json:"reason,omitempty"`
	// PlatformInstances is the deterministic progress view for each platform instance.
	PlatformInstances []PlatformInstanceRegistrationStatus `json:"platform_instances"`
}

// RegistrationConflict is the resolved duplicate-claim view for one unit key.
// Winner and Losers are registration views, so their origins, effective status,
// and rejection reasons have the same meanings as the normal list response.
type RegistrationConflict struct {
	// UnitType is the unit type identifier shared by all competing proposals.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the unit identifier shared by all competing proposals.
	UnitID uint16 `json:"unit_id"`
	// ResolutionStatus is resolved when Winner is the deterministic survivor.
	ResolutionStatus string `json:"resolution_status"`
	// Winner is the proposal that remains the registration for this unit key.
	Winner Registration `json:"winner"`
	// Losers are competing proposals rejected with registration_key_conflict.
	Losers []Registration `json:"losers"`
}

// PlatformInstanceRegistrationStatus is one platform instance's progress for a
// registration request.
type PlatformInstanceRegistrationStatus struct {
	// Machine is the platform instance's descriptor machine. It is the identity
	// the proposal itself names, so it is always present.
	Machine string `json:"machine"`
	// IP is the platform instance's descriptor IP. It is a display field looked
	// up in the answering node's own topology rather than carried by the
	// proposal, so it is empty for a historical proposal that names a machine the
	// deployment no longer has.
	IP string `json:"ip"`
	// Status is pending, accepted, or rejected.
	Status string `json:"status"`
	// Reason is an optional bounded machine-readable rejection code.
	Reason *string `json:"reason,omitempty"`
}

// Error is the small, consistent JSON error response returned by the HTTP API.
type Error struct {
	// Code is a stable machine-readable error code.
	Code string `json:"code"`
}

// PathParameter describes one parameter embedded in an operation path. Type is
// a zero value of the Go type used to parse it; the conformance-tests module derives
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
// conformance-tests module generates the OpenAPI specification from: change the API
// here and regenerate api-specifications/openapi.yaml.
func Describe() Contract {
	return Contract{
		Title:       "OPDL Platform API",
		Version:     "0.1.0",
		Description: "HTTP API served by the OPDL platform runtime.",
		Operations: []Operation{
			{
				Method:      http.MethodPost,
				Path:        "/registrations",
				OperationID: "registerUnit",
				Summary:     "Propose a unit registration",
				RequestBody: &RequestBody{Type: RegistrationRequest{}, Required: true},
				Responses: []Response{
					{Status: http.StatusAccepted, Body: ProposalAccepted{}},
					{Status: http.StatusBadRequest, Body: Error{}},
					{Status: http.StatusServiceUnavailable, Body: Error{}},
				},
			},
			{
				Method:      http.MethodGet,
				Path:        "/registrations/conflicts",
				OperationID: "listRegistrationConflicts",
				Summary:     "List resolved registration conflicts",
				Responses: []Response{
					{Status: http.StatusOK, Body: []RegistrationConflict{}},
					{Status: http.StatusInternalServerError, Body: Error{}},
				},
			},
			{
				Method:      http.MethodGet,
				Path:        "/registrations/{proposal_id}",
				OperationID: "getRegistrationStatus",
				Summary:     "Get a registration proposal's status",
				PathParameters: []PathParameter{
					{Name: "proposal_id", Type: "", Required: true},
				},
				Responses: []Response{
					{Status: http.StatusOK, Body: Registration{}},
					{Status: http.StatusNotFound, Body: Error{}},
				},
			},
			{
				Method:      http.MethodGet,
				Path:        "/registrations",
				OperationID: "listRegistrations",
				Summary:     "List registration proposals",
				Responses: []Response{
					{Status: http.StatusOK, Body: []Registration{}},
				},
			},
		},
	}
}
