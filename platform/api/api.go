package api

import "errors"

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

	// InstanceRolePrimary identifies the primary fixed instance role.
	InstanceRolePrimary = "Primary"
	// InstanceRoleStandby identifies the standby fixed instance role.
	InstanceRoleStandby = "Standby"

	// HealthStatusHealthy indicates all responsibilities are performing safely.
	HealthStatusHealthy = "Healthy"
	// HealthStatusDegraded indicates reduced capability without requiring failover.
	HealthStatusDegraded = "Degraded"
	// HealthStatusUnhealthy indicates inability to reliably perform active duties.
	HealthStatusUnhealthy = "Unhealthy"

	// LeaseStateOwned indicates primary ownership lease is held.
	LeaseStateOwned = "Owned"
	// LeaseStateUnowned indicates primary ownership lease is not held.
	LeaseStateUnowned = "Unowned"
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

// ErrNotImplemented indicates that a requested operation is not implemented.
var ErrNotImplemented = errors.New("api: operation not implemented")

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

// HealthResponse is the primary operational health assessment.
type HealthResponse struct {
	// Status is Healthy, Degraded, or Unhealthy.
	Status string `json:"status" doc:"Overall health status: Healthy, Degraded, or Unhealthy." example:"Healthy"`
	// InstanceID is the instance identifier.
	InstanceID string `json:"instanceId" doc:"Instance identifier." example:"Primary"`
	// Role is the instance role: Primary or Standby.
	Role string `json:"role" doc:"Instance role: Primary or Standby." example:"Primary"`
	// RuntimeState is Active or Passive.
	RuntimeState string `json:"runtimeState" doc:"Current runtime state: Active or Passive." example:"Active"`
	// Version is the platform runtime version.
	Version string `json:"version" doc:"Platform runtime version." example:"0.1.0"`
	// Uptime is the human-readable process uptime.
	Uptime string `json:"uptime" doc:"Human-readable process uptime." example:"14d 07h 23m"`
	// Checks contains dependency health check results.
	Checks map[string]string `json:"checks,omitempty" doc:"Dependency health check results."`
}

// HealthLiveResponse is the process liveness assessment.
type HealthLiveResponse struct {
	// Status is Healthy or Unhealthy.
	Status string `json:"status" doc:"Process liveness status." example:"Healthy"`
}

// HealthReadyResponse is the operational readiness assessment.
type HealthReadyResponse struct {
	// Status is Healthy, Degraded, or Unhealthy.
	Status string `json:"status" doc:"Operational readiness status." example:"Healthy"`
}

// HealthHAResponse is the high-availability and ownership diagnostics view.
type HealthHAResponse struct {
	// Role is Primary or Standby.
	Role string `json:"role" doc:"Instance role: Primary or Standby." example:"Primary"`
	// RuntimeState is Active or Passive.
	RuntimeState string `json:"runtimeState" doc:"Current runtime state: Active or Passive." example:"Active"`
	// LeaseState is Owned or Unowned.
	LeaseState string `json:"leaseState" doc:"Lease ownership status: Owned or Unowned." example:"Owned"`
	// LeaseExpirationUTC is an optional ISO-8601 timestamp for lease expiration in UTC.
	LeaseExpirationUTC *string `json:"leaseExpirationUtc,omitempty" doc:"Lease expiration timestamp in UTC." example:"2026-07-24T10:15:00Z"`
}
