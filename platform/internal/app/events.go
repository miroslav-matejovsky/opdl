package app

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// This file declares runtime lifecycle, API, and standby facts. Every event is
// instance-scoped and uses the default scope. Catalog tests stamp the full list
// and verify that invariant.
//
// Startup and shutdown propagate publication failures. Background callbacks use
// events.BestEffort because they have no caller to return an error to.

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

	// TypeEventFabricStarted is stated once the instance's embedded broker is
	// running and its own client is connected to it.
	TypeEventFabricStarted events.Type = "platform.app.event_fabric_started"
	// TypeEventFabricStartFailed is stated when either of those could not be
	// brought up.
	TypeEventFabricStartFailed events.Type = "platform.app.event_fabric_start_failed"

	// TypeStandbyWaiting is stated when a standby is waiting for ownership.
	TypeStandbyWaiting events.Type = "platform.app.standby_waiting"
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
	// MachineEventsFile is the machine's shared store, which this process also
	// appends its machine-scoped events to. It is stated here because it is the
	// other file this process opened before it could state anything, and because
	// it is where an operator reads the machine's account rather than one
	// instance's.
	MachineEventsFile string `json:"machine_events_file"`
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
	// Reason is why a new incarnation began, one of the state.Reason
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
	// state.Reason values.
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

// EventFabricStarted states that this instance's embedded broker is running and
// its own client is connected to it.
//
// Both instances of a machine state it, because both run their own broker for
// their whole lifetime. It is the fact an operator reads to learn which server
// name and which cluster address belong to which process, and which peers that
// process set out to join.
//
// It says the broker is up and reachable, not that the site's cluster has
// formed. Routes are dialed in the background and a peer that is down is
// retried, so membership is a later and separate thing to observe.
type EventFabricStarted struct {
	// ServerName is the embedded broker's identity, unique within the project.
	ServerName string `json:"server_name"`
	// ClusterName is the NATS cluster it belongs to, which is the site's.
	ClusterName string `json:"cluster_name"`
	// ClusterAddress is where its peers reach it, on the machine's own ip. It is
	// the only address the broker binds: the platform's own client connects in
	// process.
	ClusterAddress string `json:"cluster_address"`
	// Routes are the peers it dials, as the descriptor stated them. Empty on a
	// site that deploys one instance in total.
	Routes []string `json:"routes,omitempty"`
}

// EventType returns the event's stable dotted kind.
func (EventFabricStarted) EventType() events.Type { return TypeEventFabricStarted }

// EventFabricStartFailed states that this instance could not bring its event
// fabric up, either because the broker would not start or because the client
// would not connect to it.
type EventFabricStartFailed struct {
	// ServerName is the embedded broker's identity, as the descriptor stated it.
	ServerName string `json:"server_name"`
	// ClusterName is the cluster it was to join, as the descriptor stated it.
	ClusterName string `json:"cluster_name"`
	// ClusterAddress is the address it was to bind, as the descriptor stated it.
	// A port already taken is the usual reason it did not.
	ClusterAddress string `json:"cluster_address"`
	// Error is what went wrong.
	Error string `json:"error"`
}

// EventType returns the event's stable dotted kind.
func (EventFabricStartFailed) EventType() events.Type { return TypeEventFabricStartFailed }

// Severity reports an instance with no event fabric at all as an error. It stops
// rather than running detached from the site.
func (EventFabricStartFailed) Severity() events.Severity { return events.SeverityError }

// StandbyWaiting states that a standby is waiting for Primary Ownership. It
// carries no payload: the fact is the whole of it, and which process it is about
// is in the envelope's origin.
type StandbyWaiting struct{}

// EventType returns the event's stable dotted kind.
func (StandbyWaiting) EventType() events.Type { return TypeStandbyWaiting }
