package api

// The statuses a deployed service unit can have.
//
// They are their own constants rather than the platform's HealthStatus* values,
// even where the strings coincide, because they answer a different question. The
// platform statuses describe this instance and decide whether its peer may
// promote through it; these describe a service the platform watches and decide
// nothing. Sharing the identifiers would invite one to be passed where the other
// is meant, which is precisely the confusion the plan forbids.
const (
	// ServiceStatusUnknown is a service nothing current says anything about:
	// nobody has reported on it, every report about it has expired, or the
	// observers that did report had not resolved it yet.
	ServiceStatusUnknown = "Unknown"
	// ServiceStatusHealthy is a service every current observer found well.
	ServiceStatusHealthy = "Healthy"
	// ServiceStatusUnhealthy is a service every current observer found failing.
	ServiceStatusUnhealthy = "Unhealthy"
	// ServiceStatusDegraded is a service whose current observers disagree. No
	// observer can report it; it exists only as the result of reducing several.
	ServiceStatusDegraded = "Degraded"
)

// The states of this instance's half of the site's health traffic.
//
// They describe whether observations from elsewhere are arriving, which the
// platform's own eventFabric check cannot say: that one round-trips a message
// through this instance's embedded broker and so proves only that the local
// broker works. A site whose routes are down passes it while every remote
// observer goes silent.
const (
	// DistributionConnected means every expected observer on another machine is
	// reporting and current.
	DistributionConnected = "Connected"
	// DistributionPartial means some expected remote observers are current and
	// others are missing or stale.
	DistributionPartial = "Partial"
	// DistributionIsolated means no expected remote observer is current. This
	// instance still knows its own machine; it has stopped hearing about the rest
	// of the site.
	DistributionIsolated = "Isolated"
	// DistributionLocal means the site expects no observer on another machine, so
	// there is no remote traffic whose absence would mean anything.
	DistributionLocal = "Local"
)

// ServiceHealthResponse is one instance's whole view of the site's deployed
// services.
//
// Every instance builds this from the same static inventory and the same
// observations, so two instances that have received the same reports answer with
// the same content. It is query data: a site full of failing services is
// answered with 200, because the platform succeeded in saying so.
type ServiceHealthResponse struct {
	// Project, Environment, and Site name the deployment this view covers. They
	// are carried so a response pasted somewhere else is still self-describing.
	Project     string `json:"project" doc:"Project this view covers." example:"customer-a"`
	Environment string `json:"environment" doc:"Environment this view covers." example:"production"`
	Site        string `json:"site" doc:"Site this view covers." example:"north"`
	// Machine and Role identify the instance that answered. Two instances of one
	// machine hold separate views and may briefly differ, so which one produced a
	// snapshot is part of reading it.
	Machine string `json:"machine" doc:"Machine of the instance that produced this view." example:"local-server"`
	Role    string `json:"role" doc:"Fixed instance role that produced this view: primary or standby." example:"primary"`
	// GeneratedAtUTC is when this snapshot was taken, on the answering instance's
	// clock. Freshness below is measured against it.
	GeneratedAtUTC string `json:"generatedAtUtc" doc:"When this snapshot was taken, in UTC." example:"2026-07-28T12:00:00Z"`
	// Summary counts the services by status.
	Summary ServiceHealthSummary `json:"summary" doc:"Service counts by status."`
	// Services is every service the site's inventory contains, whether or not
	// anything is known about it, in inventory order.
	Services []ServiceHealthUnit `json:"services" doc:"Every service at this site, in inventory order."`
	// Distribution is what this instance's health traffic is doing.
	Distribution ServiceHealthDistribution `json:"distribution" doc:"State of this instance's site health traffic."`
}

// ServiceHealthSummary counts services by their reduced status.
type ServiceHealthSummary struct {
	Healthy   int `json:"healthy" doc:"Services every current observer found well." example:"2"`
	Unhealthy int `json:"unhealthy" doc:"Services every current observer found failing." example:"1"`
	Degraded  int `json:"degraded" doc:"Services whose current observers disagree." example:"0"`
	Unknown   int `json:"unknown" doc:"Services nothing current says anything about." example:"1"`
}

// ServiceHealthUnit is one deployed service and what is currently known about it.
//
// The observer lists are what separate a failing service from a platform that
// has stopped watching one. A service nobody is reporting on is Unknown for a
// reason the platform owns, and saying which observers are absent is the only
// way an operator can tell that from a service that is genuinely unreachable.
type ServiceHealthUnit struct {
	Machine        string `json:"machine" doc:"Machine hosting this service." example:"local-server"`
	MachineProfile string `json:"machineProfile" doc:"Purpose of the hosting machine." example:"local-server"`
	Service        string `json:"service" doc:"Service name." example:"alarm-service"`
	ServiceRole    string `json:"serviceRole" doc:"Part this copy plays: master or slave." example:"master"`
	// Status is the reduction of every current observation below: Healthy,
	// Unhealthy, Degraded, or Unknown.
	Status string `json:"status" doc:"Reduced service status: Healthy, Unhealthy, Degraded, or Unknown." example:"Healthy"`
	// ExpectedObservers are the platform instance roles that should be reporting
	// on this service, sorted.
	ExpectedObservers []string `json:"expectedObservers" doc:"Platform instance roles expected to report on this service." example:"[\"primary\",\"standby\"]"`
	// MissingObservers are expected observers that have never reported here.
	MissingObservers []string `json:"missingObservers" doc:"Expected observers that have never reported."`
	// StaleObservers are expected observers whose last report has expired. They
	// are listed apart from the missing ones because an observer that stopped
	// talking and one that never started are different faults.
	StaleObservers []string `json:"staleObservers" doc:"Expected observers whose last report has expired."`
	// Observations is what each observer last said, current or not, ordered by
	// observer role.
	Observations []ServiceHealthObservation `json:"observations" doc:"Last report from each observer, ordered by observer role."`
}

// ServiceHealthObservation is one observer's last report about one service.
type ServiceHealthObservation struct {
	ObserverRole string `json:"observerRole" doc:"Platform instance role that reported: primary or standby." example:"primary"`
	// Status is what that observer found: Healthy, Unhealthy, or Unknown. Degraded
	// never appears here — it describes a disagreement between observers, which no
	// single observer can see.
	Status string `json:"status" doc:"Status this observer reported: Healthy, Unhealthy, or Unknown." example:"Healthy"`
	// Stale reports whether this observation has passed the service's freshness
	// bound. A stale observation is shown rather than removed: what it last said
	// is the most recent thing anyone here knows, and that it has aged is a
	// separate fact.
	Stale bool `json:"stale" doc:"Whether this report has passed the service's freshness bound." example:"false"`
	// CheckedAtUTC is when the observer says it probed, and ReceivedAtUTC is when
	// this instance heard about it. Both are shown because a large gap between
	// them is either a slow site or a machine whose clock is wrong, and neither
	// one alone tells an operator which.
	CheckedAtUTC  string `json:"checkedAtUtc" doc:"When the observer says it probed, in UTC." example:"2026-07-28T11:59:58Z"`
	ReceivedAtUTC string `json:"receivedAtUtc" doc:"When this instance received the report, in UTC." example:"2026-07-28T11:59:58Z"`
	// AgeMs is how long ago the report arrived, on this instance's clock. It is
	// what Stale is decided from, so it is shown beside it.
	AgeMs int64 `json:"ageMs" doc:"Milliseconds since this report arrived, on the answering instance's clock." example:"1200"`
	// LatencyMs is how long the observer's own probe took.
	LatencyMs int64 `json:"latencyMs" doc:"Milliseconds the observer's probe took." example:"4"`
	// ConsecutiveFailures is how many attempts had failed in a row when the
	// observer reported. It is what shows a service on its way down before its
	// status has moved.
	ConsecutiveFailures int `json:"consecutiveFailures" doc:"Attempts that had failed in a row when the observer reported." example:"0"`
	// Error is why the observer's probe failed, empty when it succeeded.
	Error string `json:"error,omitempty" doc:"Why the observer's probe failed, absent when it succeeded."`
}

// ServiceHealthDistribution is what this instance's health traffic is doing.
//
// It is here rather than in the platform's own health because it is a statement
// about this view: an operator reading a site full of Unknown services needs to
// know whether the site is failing or whether this instance has stopped hearing
// from it, and those two look identical without this.
type ServiceHealthDistribution struct {
	// State is Connected, Partial, Isolated, or Local.
	State string `json:"state" doc:"Whether expected remote observations are arriving: Connected, Partial, Isolated, or Local." example:"Connected"`
	// Published is how many of this instance's observations reached the
	// connection, Superseded how many were replaced by a newer report about the
	// same service before they were sent, and PublishFailed how many the
	// connection refused.
	Published     int64 `json:"published" doc:"Observations from this instance that reached the connection." example:"120"`
	Superseded    int64 `json:"superseded" doc:"Observations replaced by a newer one before being sent." example:"0"`
	PublishFailed int64 `json:"publishFailed" doc:"Observations the connection refused." example:"0"`
	// Delivered is how many messages arrived on the health subject, whatever
	// became of them. It is what tells a silent site from one whose messages are
	// all being discarded.
	Delivered int64 `json:"delivered" doc:"Messages received on the health subject." example:"240"`
	// Rejected counts messages that were not well-formed reports about this
	// deployment, and Dropped counts well-formed reports the view did not apply.
	// Both are lists rather than maps so a generated client gets a usable type and
	// so a new reason does not change the schema.
	Rejected []ServiceHealthCount `json:"rejected" doc:"Messages rejected before decoding, by reason."`
	Dropped  []ServiceHealthCount `json:"dropped" doc:"Decoded reports the view did not apply, by reason."`
}

// ServiceHealthCount is one reason and how often it has happened.
type ServiceHealthCount struct {
	Reason string `json:"reason" doc:"Why the message was not applied." example:"stale"`
	Count  int64  `json:"count" doc:"How many times it has happened." example:"3"`
}
