package api

import "errors"

const (
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

	// HealthCheckConfiguration names the configuration entry in
	// HealthResponse.Checks.
	HealthCheckConfiguration = "configuration"
	// HealthCheckServiceMonitor names the service monitor entry in
	// HealthResponse.Checks. It reports this instance's own watching of the
	// machine's services — whether its probes are still producing observations
	// its view accepts — and never what those probes found. A failing target is a
	// fact about the target, reported by GET /health/services; both of a machine's
	// instances can see it, so letting it reach this map would offer an operator a
	// failover that repairs nothing.
	HealthCheckServiceMonitor = "serviceMonitor"
	// HealthCheckEventFabric names the event fabric entry in
	// HealthResponse.Checks. It reports whether this instance's embedded broker
	// is carrying messages, proven by a round trip through it rather than by a
	// connection status.
	HealthCheckEventFabric = "eventFabric"
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
