package api

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

	// InstanceStateActive means this instance holds Primary Ownership and serves
	// the whole API.
	InstanceStateActive = "active"
	// InstanceStatePassive means the machine's other instance holds Primary
	// Ownership. A Passive instance answers for itself and refuses every domain
	// operation.
	InstanceStatePassive = "passive"
)

// Instance is what one platform instance reports about itself.
//
// Every instance serves this for its whole lifetime, whether it is Active or
// Passive, which is the point of it: an operator can ask a Passive instance what
// it is and where the other one is, and a Passive instance has no other answer to
// give. Nothing here comes from the site journal, so it is answerable before the
// instance's projection has caught up, and while it never will.
type Instance struct {
	// Machine is the descriptor machine both of this pair's instances run on.
	Machine string `json:"machine" doc:"Descriptor machine this instance runs on." example:"local-server"`
	// Role is the instance's fixed, build-time role: primary or standby. It never
	// changes, and it is not what decides who serves.
	Role string `json:"role" doc:"Fixed build-time instance role: primary or standby." example:"primary"`
	// State is what this instance is doing now: active or passive. It is the half
	// that changes, and it changes only by Primary Ownership moving.
	State string `json:"state" doc:"Current runtime state: active or passive." example:"active"`
	// Address is where this instance serves this API. It is always on loopback.
	Address string `json:"address" doc:"This instance's own loopback API address." example:"127.0.0.1:8080"`
	// PeerAddress is where the machine's other instance serves its own API, empty
	// on a machine that deploys only a Primary Instance.
	//
	// While this instance is Passive the other one holds ownership, so this is
	// where a refused domain request should be sent.
	PeerAddress string `json:"peer_address,omitempty" doc:"The machine's other instance's API address, if one is deployed." example:"127.0.0.1:8081"`
}

// RegistrationRequest is the client-supplied request to register one unit.
// Machine, IP, registration status, and platform-instance progress are supplied
// only by the platform in Registration responses.
//
// Registration is asynchronous. A valid request is durably recorded and answered
// with a ProposalAccepted; whether the site registers the unit is decided
// afterwards and read back through the proposal's status.
type RegistrationRequest struct {
	// UnitType is the unit type identifier from 0 through 255.
	UnitType uint8 `json:"unit_type" doc:"Unit type identifier, 0 through 255." minimum:"0" maximum:"255" example:"7"`
	// UnitID is the unit identifier from 0 through 65535.
	UnitID uint16 `json:"unit_id" doc:"Unit identifier, 0 through 65535." minimum:"0" maximum:"65535" example:"42"`
	// UnitTypeNameAdvertised is the required non-blank advertised unit type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised" doc:"Advertised unit type name. Required and non-blank." example:"Billing"`
	// Role is the optional Master or Slave role advertised by the unit.
	Role *string `json:"role,omitempty" doc:"Optional Master or Slave role advertised by the unit." example:"Master"`
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
	ProposalID string `json:"proposal_id" doc:"Stable proposal identity and status key." example:"c1a2b3"`
	// Sequence is the proposal's position in the site journal.
	Sequence uint64 `json:"sequence" doc:"Proposal position in the site journal." example:"12"`
}

// Registration is the platform's immutable view of one registration request.
type Registration struct {
	// ProposalID is the proposal's stable identity and its status key.
	ProposalID string `json:"proposal_id" doc:"Stable proposal identity and status key." example:"c1a2b3"`
	// UnitType is the registered unit type identifier.
	UnitType uint8 `json:"unit_type" doc:"Registered unit type identifier." minimum:"0" maximum:"255" example:"7"`
	// UnitID is the registered unit identifier.
	UnitID uint16 `json:"unit_id" doc:"Registered unit identifier." minimum:"0" maximum:"65535" example:"42"`
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised" doc:"Advertised unit type name." example:"Billing"`
	// Role is the optional Master or Slave role advertised by the unit.
	Role *string `json:"role,omitempty" doc:"Optional Master or Slave role advertised by the unit." example:"Master"`
	// Machine is the descriptor machine where the request originated.
	Machine string `json:"machine" doc:"Descriptor machine where the request originated." example:"site-1"`
	// IP is the descriptor IP where the request originated.
	IP string `json:"ip" doc:"Descriptor IP where the request originated." example:"10.0.0.11"`
	// Status is pending, accepted, or rejected.
	Status string `json:"status" doc:"Registration status: pending, accepted, or rejected." example:"accepted"`
	// Reason is an optional bounded machine-readable rejection code.
	Reason *string `json:"reason,omitempty" doc:"Optional machine-readable rejection code." example:"registration_key_conflict"`
	// PlatformInstances is the deterministic progress view for each platform instance.
	PlatformInstances []PlatformInstanceRegistrationStatus `json:"platform_instances" doc:"Deterministic progress view for each platform instance."`
}

// RegistrationConflict is the resolved duplicate-claim view for one unit key.
// Winner and Losers are registration views, so their origins, effective status,
// and rejection reasons have the same meanings as the normal list response.
type RegistrationConflict struct {
	// UnitType is the unit type identifier shared by all competing proposals.
	UnitType uint8 `json:"unit_type" doc:"Unit type identifier shared by all competing proposals." minimum:"0" maximum:"255" example:"7"`
	// UnitID is the unit identifier shared by all competing proposals.
	UnitID uint16 `json:"unit_id" doc:"Unit identifier shared by all competing proposals." minimum:"0" maximum:"65535" example:"42"`
	// ResolutionStatus is resolved when Winner is the deterministic survivor.
	ResolutionStatus string `json:"resolution_status" doc:"Resolution status of the conflict, resolved when the winner is the deterministic survivor." example:"resolved"`
	// Winner is the proposal that remains the registration for this unit key.
	Winner Registration `json:"winner" doc:"Proposal that remains the registration for this unit key."`
	// Losers are competing proposals rejected with registration_key_conflict.
	Losers []Registration `json:"losers" doc:"Competing proposals rejected with registration_key_conflict."`
}

// PlatformInstanceRegistrationStatus is one platform instance's progress for a
// registration request.
type PlatformInstanceRegistrationStatus struct {
	// Machine is the platform instance's descriptor machine. It is the identity
	// the proposal itself names, so it is always present.
	Machine string `json:"machine" doc:"Platform instance's descriptor machine." example:"node-a"`
	// IP is the platform instance's descriptor IP. It is a display field looked
	// up in the answering node's own topology rather than carried by the
	// proposal, so it is empty for a historical proposal that names a machine the
	// deployment no longer has.
	IP string `json:"ip" doc:"Platform instance's descriptor IP, empty when the deployment no longer has the machine." example:"10.0.0.21"`
	// Status is pending, accepted, or rejected.
	Status string `json:"status" doc:"Platform instance's registration status: pending, accepted, or rejected." example:"accepted"`
	// Reason is an optional bounded machine-readable rejection code.
	Reason *string `json:"reason,omitempty" doc:"Optional machine-readable rejection code." example:"registration_key_conflict"`
}
