package app

import "github.com/miroslav-matejovsky/opdl/platform/internal/instance/events"

// This file is the application runtime's event catalog: every fact composition
// itself can state. They are the process's own story — what it started, bound,
// opened, waited for, and stopped — as opposed to the domain facts the site
// journal carries.
//
// Process:
//
//   - platform.app.process_started: the process read its configuration, settled
//     its role, and began running.
//   - platform.app.process_stopped: the process finished, cleanly or not.
//   - platform.app.epoch_advanced: the instance began a new incarnation.
//   - platform.app.epoch_advance_failed: it could not record that it had.
//
// Local resources:
//
//   - platform.app.lease_open_failed: the Primary Ownership lease could not be
//     opened, so this instance cannot take part in ownership.
//
// HTTP API:
//
//   - platform.app.api_listen_failed: the instance could not bind its address.
//   - platform.app.api_listening: the instance is bound and answering about
//     itself, whether or not it is active.
//   - platform.app.api_active: the instance is serving domain operations.
//   - platform.app.api_stopped: the instance stopped serving.
//
// Standby and projection:
//
//   - platform.app.standby_waiting: a standby is caught up and waiting for
//     Primary Ownership.
//   - platform.app.standby_open_retry: a standby could not open its projection
//     and is retrying while it waits.
//   - platform.app.standby_ready: a standby's projection caught up.
//   - platform.app.projection_caught_up: a projection reached a captured journal
//     high-water mark.
//   - platform.app.projection_lag_exceeded: a projection fell further behind the
//     journal than serving allows.
//   - platform.app.failover_readiness_changed: an instance became, or stopped
//     being, current enough to take over.
//
// Site:
//
//   - platform.app.site_opening: this node's Event Fabric composition started.
//   - platform.app.site_open_failed: it did not reach the state it needed.
//   - platform.app.site_ready: an active site is ready to serve.
//   - platform.app.site_stopping: a site began releasing.
//   - platform.app.site_stopped: a site finished releasing.
//   - platform.app.background_loop_stopped: a projector or handler loop ended.
//
// These are stated through the process-local publisher, whose only backend is
// the mandatory local JSONL record, rather than through the site's fan-out
// publisher. They describe one process, and a process that is failing to start
// is exactly the one that cannot write to a journal.
//
// Where a failure to state one goes depends on what the stating code can do
// about it. Startup and shutdown return or join it, because a process that could
// not write its local record has not started or stopped cleanly. The background
// loops and the status callbacks have no caller to return to, so theirs go
// through an events.Diagnostic and reach the process error stream instead: a
// diagnostic about a broken pipeline must not travel down that pipeline.

const (
	// TypeProcessStarted is stated once the process knows its role and identity.
	TypeProcessStarted events.Type = "platform.app.process_started"
	// TypeProcessStopped is stated last, whatever the outcome.
	TypeProcessStopped events.Type = "platform.app.process_stopped"

	// TypeEpochAdvanced is stated when the instance begins a new incarnation.
	TypeEpochAdvanced events.Type = "platform.app.epoch_advanced"
	// TypeEpochAdvanceFailed is stated when a new incarnation could not be
	// recorded in the instance's state file.
	TypeEpochAdvanceFailed events.Type = "platform.app.epoch_advance_failed"

	// TypeLeaseOpenFailed is stated when the ownership lease cannot be opened.
	TypeLeaseOpenFailed events.Type = "platform.app.lease_open_failed"

	// TypeAPIListenFailed is stated when the instance cannot bind its address.
	TypeAPIListenFailed events.Type = "platform.app.api_listen_failed"
	// TypeAPIListening is stated once the instance answers about itself.
	TypeAPIListening events.Type = "platform.app.api_listening"
	// TypeAPIActive is stated once the instance serves domain operations.
	TypeAPIActive events.Type = "platform.app.api_active"
	// TypeAPIStopped is stated when the instance stops serving.
	TypeAPIStopped events.Type = "platform.app.api_stopped"

	// TypeStandbyWaiting is stated when a standby is caught up and waiting.
	TypeStandbyWaiting events.Type = "platform.app.standby_waiting"
	// TypeStandbyOpenRetry is stated while a standby retries its projection.
	TypeStandbyOpenRetry events.Type = "platform.app.standby_open_retry"
	// TypeStandbyReady is stated when a standby's projection has caught up.
	TypeStandbyReady events.Type = "platform.app.standby_ready"
	// TypeProjectionCaughtUp is stated when a projection reaches a captured mark.
	TypeProjectionCaughtUp events.Type = "platform.app.projection_caught_up"
	// TypeProjectionLagExceeded is stated when lag crosses the serving bound.
	TypeProjectionLagExceeded events.Type = "platform.app.projection_lag_exceeded"
	// TypeFailoverReadinessChanged is stated when an instance's readiness to take
	// over changes.
	TypeFailoverReadinessChanged events.Type = "platform.app.failover_readiness_changed"

	// TypeSiteOpening is stated when this node's site composition starts.
	TypeSiteOpening events.Type = "platform.app.site_opening"
	// TypeSiteOpenFailed is stated when that composition does not complete.
	TypeSiteOpenFailed events.Type = "platform.app.site_open_failed"
	// TypeSiteReady is stated when an active site can serve.
	TypeSiteReady events.Type = "platform.app.site_ready"
	// TypeSiteStopping is stated when a site begins releasing.
	TypeSiteStopping events.Type = "platform.app.site_stopping"
	// TypeSiteStopped is stated when a site has released.
	TypeSiteStopped events.Type = "platform.app.site_stopped"
	// TypeBackgroundLoopStopped is stated when a projector or handler loop ends.
	TypeBackgroundLoopStopped events.Type = "platform.app.background_loop_stopped"
)

// Site open phases. A site opens in stages, and which one it got to is what an
// operator needs to know from a failure: a configuration problem, a transport
// that would not open, and a projection that would not catch up are three
// different faults with the same outcome.
const (
	// PhaseConfiguration is deriving the Event Fabric configuration.
	PhaseConfiguration = "configuration"
	// PhaseEventFabric is opening the transport and its journal.
	PhaseEventFabric = "event_fabric"
	// PhaseStandbyCatchUp is a standby catching its projection up.
	PhaseStandbyCatchUp = "standby_catch_up"
	// PhaseActiveReadiness is an active node completing its readiness sequence.
	PhaseActiveReadiness = "active_readiness"
)

// failureSeverity ranks a fact that carries an optional error: an operation that
// ended with one is an error, and the same operation without one is routine.
// Whether it failed is in the payload, so the severity is read from there rather
// than stated twice.
func failureSeverity(err string) events.Severity {
	if err != "" {
		return events.SeverityError
	}
	return events.SeverityInfo
}

// ProcessStarted states that the platform process began running.
type ProcessStarted struct {
	// EventsFile is the mandatory local JSONL record this instance appends every
	// event to. It is what an operator opens next.
	EventsFile string `json:"events_file"`
	// StateFile is the instance's durable state record, which carries the epoch
	// below across restarts and crashes.
	StateFile string `json:"state_file"`
	// Epoch is this instance's incarnation number, already advanced for this
	// process. It is what tells this run of the instance from the previous one.
	Epoch uint64 `json:"epoch"`
	// StandbyEnabled reports whether this machine deploys a warm standby.
	StandbyEnabled bool `json:"standby_enabled"`
}

// EventType returns the event's stable dotted kind.
func (ProcessStarted) EventType() events.Type { return TypeProcessStarted }

// ProcessStopped states that the platform process finished.
type ProcessStopped struct {
	// Error is why the process failed, empty when it stopped cleanly.
	Error string `json:"error,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (ProcessStopped) EventType() events.Type { return TypeProcessStopped }

// Severity reports a failed run as an error and a clean stop as routine.
func (e ProcessStopped) Severity() events.Severity { return failureSeverity(e.Error) }

// EpochAdvanced states that the instance began a new incarnation.
//
// The epoch is monotonic for the whole life of a deployed instance, so reading
// the last one an instance stated is reading which incarnation is current. Which
// instance it is about is the envelope's origin.
//
// It carries both per-kind counts as well as the total, so a reader of the
// record can tell a machine whose ownership keeps moving from one whose process
// keeps dying without opening the state file the counts came from.
type EpochAdvanced struct {
	// StateFile is the durable record the epoch was written to.
	StateFile string `json:"state_file"`
	// Epoch is the new incarnation number, one higher than the previous.
	Epoch uint64 `json:"epoch"`
	// Reason is why a new incarnation began, one of the instancestate.Reason
	// values.
	Reason string `json:"reason"`
	// ProcessEpoch is how many times this instance has been launched, ever.
	ProcessEpoch uint64 `json:"process_epoch"`
	// ActivationEpoch is how many times it has taken Primary Ownership, ever.
	ActivationEpoch uint64 `json:"activation_epoch"`
}

// EventType returns the event's stable dotted kind.
func (EpochAdvanced) EventType() events.Type { return TypeEpochAdvanced }

// EpochAdvanceFailed states that a new incarnation could not be recorded.
//
// It is an error rather than a degradation. An instance whose epoch is not
// durable cannot be ordered against its own past incarnations, so it stops
// rather than running under a number a restart would hand out again.
type EpochAdvanceFailed struct {
	// StateFile is the durable record that could not be written.
	StateFile string `json:"state_file"`
	// Reason is which new incarnation was being recorded, one of the
	// instancestate.Reason values.
	Reason string `json:"reason"`
	// Error is why it could not be recorded.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (EpochAdvanceFailed) EventType() events.Type { return TypeEpochAdvanceFailed }

// Severity reports an instance that cannot record its incarnation as an error.
func (EpochAdvanceFailed) Severity() events.Severity { return events.SeverityError }

// LeaseOpenFailed states that the Primary Ownership lease could not be opened.
type LeaseOpenFailed struct {
	// File is the machine-wide lease file that could not be prepared.
	File string `json:"file"`
	// Error is why it could not be opened.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (LeaseOpenFailed) EventType() events.Type { return TypeLeaseOpenFailed }

// Severity reports an instance that cannot take part in ownership as an error.
func (LeaseOpenFailed) Severity() events.Severity { return events.SeverityError }

// APIListenFailed states that the instance could not bind its API address.
type APIListenFailed struct {
	// Address is the host:port this instance binds for its whole lifetime.
	Address string `json:"address"`
	// Error is why the bind failed.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (APIListenFailed) EventType() events.Type { return TypeAPIListenFailed }

// Severity reports an instance nobody can reach as an error.
func (APIListenFailed) Severity() events.Severity { return events.SeverityError }

// APIListening states that the instance is bound and answering about itself.
type APIListening struct {
	// Address is the host:port the instance bound.
	Address string `json:"address"`
	// InstanceState is what the instance is doing: passive here, because an
	// instance answers about itself before it is ever active.
	InstanceState string `json:"instance_state"`
}

// EventType returns the event's stable dotted kind.
func (APIListening) EventType() events.Type { return TypeAPIListening }

// APIActive states that the instance is serving domain operations.
type APIActive struct {
	// Address is the host:port the instance serves on. It is the same address it
	// bound while passive: activation swaps the handler, not the endpoint.
	Address string `json:"address"`
	// InstanceState is active.
	InstanceState string `json:"instance_state"`
}

// EventType returns the event's stable dotted kind.
func (APIActive) EventType() events.Type { return TypeAPIActive }

// APIStopped states that the instance stopped serving.
type APIStopped struct {
	// Error is what went wrong while serving or stopping, empty when the
	// instance stopped cleanly.
	Error string `json:"error,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (APIStopped) EventType() events.Type { return TypeAPIStopped }

// Severity reports a failed stop as an error and a clean one as routine.
func (e APIStopped) Severity() events.Severity { return failureSeverity(e.Error) }

// StandbyWaiting states that a standby is caught up and waiting for Primary
// Ownership. It carries no payload: the fact is the whole of it, and which
// process it is about is in the envelope's origin.
type StandbyWaiting struct{}

// EventType returns the event's stable dotted kind.
func (StandbyWaiting) EventType() events.Type { return TypeStandbyWaiting }

// StandbyOpenRetry states that a standby could not open its projection and is
// retrying while it waits for ownership.
type StandbyOpenRetry struct {
	// Attempt is which try this is, counted from one.
	Attempt int `json:"attempt"`
	// Error is why the projection would not open.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (StandbyOpenRetry) EventType() events.Type { return TypeStandbyOpenRetry }

// Severity reports a standby that is not yet following the journal as a
// degradation: the machine still has an instance, but not a warm one.
func (StandbyOpenRetry) Severity() events.Severity { return events.SeverityWarn }

// StandbyReady states that a standby's projection has caught up.
type StandbyReady struct {
	// AppliedSequence is the journal sequence the projection had applied.
	AppliedSequence uint64 `json:"applied_sequence"`
	// DurationMS is how long the standby took to reach it, from the start of the
	// site composition.
	DurationMS int64 `json:"duration_ms"`
}

// EventType returns the event's stable dotted kind.
func (StandbyReady) EventType() events.Type { return TypeStandbyReady }

// ProjectionCaughtUp states that a projection reached a captured journal
// high-water mark.
type ProjectionCaughtUp struct {
	// Phase names what the node was waiting to catch up with.
	Phase string `json:"phase"`
	// HighWater is the captured mark the projection was waiting to reach.
	HighWater uint64 `json:"high_water"`
	// AppliedSequence is the sequence the projection had applied when it got
	// there, which may be past the mark on a site that kept publishing.
	AppliedSequence uint64 `json:"applied_sequence"`
	// DurationMS is how long the wait took.
	DurationMS int64 `json:"duration_ms"`
}

// EventType returns the event's stable dotted kind.
func (ProjectionCaughtUp) EventType() events.Type { return TypeProjectionCaughtUp }

// ProjectionLagExceeded states that a projection fell further behind the journal
// than serving allows, so the instance stopped serving.
type ProjectionLagExceeded struct {
	// LagBound is the configured bound the lag crossed.
	LagBound string `json:"lag_bound"`
}

// EventType returns the event's stable dotted kind.
func (ProjectionLagExceeded) EventType() events.Type { return TypeProjectionLagExceeded }

// Severity reports an instance that stopped answering as an error.
func (ProjectionLagExceeded) Severity() events.Severity { return events.SeverityError }

// FailoverReadinessChanged states that an instance became, or stopped being,
// current enough to take the machine over.
//
// It is what a service manager reads before it hands over, and it replaced the
// status file the runtime used to rewrite once a second. The facts are the same
// ones; what changed is that they are stated when they change instead of being
// restated on a timer, so the local record carries readiness transitions rather
// than a heartbeat.
//
// Which instance it is about is the envelope's origin: its role and its PID.
// Reading the last one an instance stated is reading its current readiness,
// because an instance that has not restated it has not changed it.
type FailoverReadinessChanged struct {
	// Ready reports whether this instance is current enough to take over. An
	// instance is ready until its projection has been behind the journal for
	// longer than the deployment's lag bound, or until it cannot ask its Event
	// Fabric how far behind it is.
	Ready bool `json:"ready"`
	// InstanceState is what the instance was doing when readiness changed:
	// passive while it follows the journal, active while it serves.
	InstanceState string `json:"instance_state"`
	// AppliedSequence is the highest journal sequence the projection had applied,
	// zero when the Event Fabric could not be asked.
	AppliedSequence uint64 `json:"applied_sequence"`
	// HighWater is the last sequence the journal had accepted, as this instance
	// last observed it.
	HighWater uint64 `json:"high_water"`
	// Lag is how long the projection had continuously been behind the journal, as
	// a duration string; "0s" when caught up.
	Lag string `json:"lag"`
	// Error is why the Event Fabric could not be asked, empty when it could.
	Error string `json:"error,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (FailoverReadinessChanged) EventType() events.Type { return TypeFailoverReadinessChanged }

// Severity reports a machine that has lost its ready instance as a degradation.
// A machine still has an active instance either way, but one that can no longer
// hand over has no redundancy left.
func (e FailoverReadinessChanged) Severity() events.Severity {
	if e.Ready {
		return events.SeverityInfo
	}
	return events.SeverityWarn
}

// SiteOpening states that this node's site composition started.
type SiteOpening struct {
	// Active reports whether this composition is an active node or a warm
	// standby, which decides how much of it is built.
	Active bool `json:"active"`
}

// EventType returns the event's stable dotted kind.
func (SiteOpening) EventType() events.Type { return TypeSiteOpening }

// SiteOpenFailed states that a site composition did not complete.
type SiteOpenFailed struct {
	// Phase is how far the composition got, one of the Phase constants.
	Phase string `json:"phase"`
	// Error is why it stopped there.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (SiteOpenFailed) EventType() events.Type { return TypeSiteOpenFailed }

// Severity reports a node that cannot compose its site as an error.
func (SiteOpenFailed) Severity() events.Severity { return events.SeverityError }

// SiteReady states that an active site finished its readiness sequence.
type SiteReady struct {
	// AppliedSequence is the journal sequence the projection had applied.
	AppliedSequence uint64 `json:"applied_sequence"`
	// DurationMS is how long the whole readiness sequence took.
	DurationMS int64 `json:"duration_ms"`
}

// EventType returns the event's stable dotted kind.
func (SiteReady) EventType() events.Type { return TypeSiteReady }

// SiteStopping states that a site began releasing.
type SiteStopping struct {
	// Ready reports whether this node had announced its readiness, which is what
	// makes announcing its shutdown meaningful.
	Ready bool `json:"ready"`
}

// EventType returns the event's stable dotted kind.
func (SiteStopping) EventType() events.Type { return TypeSiteStopping }

// SiteStopped states that a site finished releasing.
type SiteStopped struct {
	// Error is what failed during the release, empty when it was clean.
	Error string `json:"error,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (SiteStopped) EventType() events.Type { return TypeSiteStopped }

// Severity reports a release that did not complete as an error.
func (e SiteStopped) Severity() events.Severity { return failureSeverity(e.Error) }

// BackgroundLoopStopped states that one of the node's background Event Fabric
// loops ended. A loop that stops takes the node's serving with it, so which one
// it was and why is the first question.
type BackgroundLoopStopped struct {
	// Loop names the loop, such as "projector" or "handler registration".
	Loop string `json:"loop"`
	// Error is why it stopped, empty when it was canceled cleanly.
	Error string `json:"error,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (BackgroundLoopStopped) EventType() events.Type { return TypeBackgroundLoopStopped }

// Severity reports a loop that gave up as an error and a canceled one as
// routine.
func (e BackgroundLoopStopped) Severity() events.Severity { return failureSeverity(e.Error) }
