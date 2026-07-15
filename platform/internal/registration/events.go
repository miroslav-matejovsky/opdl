package registration

import "github.com/miroslav-matejovsky/opdl/platform/internal/events"

// This file is the registration domain's complete list of events. Each one is a
// fact this package can state because it owns the state that produced it, and
// each is recorded at the transition that makes it true:
//
//   - platform.registration.requested: a request was persisted for the first
//     time. Create records it. An exact retry does not, and a request that
//     fails validation was never persisted, so it states nothing.
//   - platform.registration.confirmed: one platform instance accepted a pending
//     request. Confirm records it, once per instance that decides.
//   - platform.registration.accepted: every expected platform instance has
//     confirmed, and the request is committed. Confirm records it after the
//     last confirmation, never for a retry of an already accepted request.
//   - platform.registration.rejected: one platform instance refused a pending
//     request. Confirm records it. Tagged warning.
//   - platform.registration.conflict: an attempt reused a registration key with
//     different immutable fields and was refused. Create records it for every
//     rejected occurrence, because the attempt changes no state and would
//     otherwise leave no trace. Tagged warning.
//
// The payloads are a published contract read from outside the process. They
// carry the origin the platform derived from its own deployment descriptor,
// never a caller's claim, and they never expose the internal proposal
// fingerprint: a request is identified by its unit key and origin.

// eventSource is the subsystem every event in this file comes from.
const eventSource = "registration"

// ReasonKeyConflict is the bounded reason on Conflict: a registration key was
// reused with different immutable fields.
const ReasonKeyConflict = "registration_key_conflict"

// TypeRequested is stated when a registration request is persisted for the
// first time.
const TypeRequested events.Type = "platform.registration.requested"

// Requested states the accepted request as stored: the four client-supplied
// fields plus the origin the platform derived from its own descriptor.
type Requested struct {
	// UnitType is the requested unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the requested unit identifier within UnitType.
	UnitID uint16 `json:"unit_id"`
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the optional advertised role, omitted when the unit advertised none.
	Role string `json:"role,omitempty"`
	// Machine is the descriptor machine that accepted the request.
	Machine string `json:"machine"`
	// IP is the descriptor IP of the machine that accepted the request.
	IP string `json:"ip"`
}

// EventType returns the event's stable dotted kind.
func (Requested) EventType() events.Type { return TypeRequested }

// Source returns the subsystem that states the fact.
func (Requested) Source() string { return eventSource }

// TypeConfirmed is stated when one platform instance accepts a pending request.
const TypeConfirmed events.Type = "platform.registration.confirmed"

// Confirmed states one platform instance's acceptance of a pending request. It
// reports that instance's decision, not the outcome of the request: acceptance
// of the request itself is Accepted.
type Confirmed struct {
	// UnitType is the confirmed unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the confirmed unit identifier within UnitType.
	UnitID uint16 `json:"unit_id"`
	// OriginMachine is the descriptor machine the request originated on.
	OriginMachine string `json:"origin_machine"`
	// ConfirmingMachine is the platform instance that accepted the request.
	ConfirmingMachine string `json:"confirming_machine"`
}

// EventType returns the event's stable dotted kind.
func (Confirmed) EventType() events.Type { return TypeConfirmed }

// Source returns the subsystem that states the fact.
func (Confirmed) Source() string { return eventSource }

// TypeAccepted is stated once every expected platform instance has confirmed a
// request.
const TypeAccepted events.Type = "platform.registration.accepted"

// Accepted states the six committed registration fields of a request that is
// now accepted.
type Accepted struct {
	// UnitType is the registered unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the registered unit identifier within UnitType.
	UnitID uint16 `json:"unit_id"`
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the optional advertised role, omitted when the unit advertised none.
	Role string `json:"role,omitempty"`
	// Machine is the descriptor machine the request originated on.
	Machine string `json:"machine"`
	// IP is the descriptor IP of the machine the request originated on.
	IP string `json:"ip"`
}

// EventType returns the event's stable dotted kind.
func (Accepted) EventType() events.Type { return TypeAccepted }

// Source returns the subsystem that states the fact.
func (Accepted) Source() string { return eventSource }

// TypeRejected is stated when a platform instance refuses a pending request.
const TypeRejected events.Type = "platform.registration.rejected"

// Rejected states one platform instance's refusal of a pending request.
type Rejected struct {
	// UnitType is the rejected unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the rejected unit identifier within UnitType.
	UnitID uint16 `json:"unit_id"`
	// OriginMachine is the descriptor machine the request originated on.
	OriginMachine string `json:"origin_machine"`
	// RejectingMachine is the platform instance that refused the request.
	RejectingMachine string `json:"rejecting_machine"`
	// Reason is the bounded machine-readable rejection code.
	Reason string `json:"reason"`
}

// EventType returns the event's stable dotted kind.
func (Rejected) EventType() events.Type { return TypeRejected }

// Source returns the subsystem that states the fact.
func (Rejected) Source() string { return eventSource }

// Tags marks the refusal as an operational anomaly.
func (Rejected) Tags() []string { return []string{events.TagWarning} }

// TypeConflict is stated for every attempt to reuse a registration key with
// different immutable fields. The stored request is left untouched, so the
// event is the only trace of the attempt.
const TypeConflict events.Type = "platform.registration.conflict"

// Fields are the immutable request and origin fields a registration key is
// compared on. Conflict carries them twice so a reader sees both sides of the
// difference without querying the platform.
type Fields struct {
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the optional advertised role, omitted when the unit advertised none.
	Role string `json:"role,omitempty"`
	// Machine is the descriptor machine of the request's origin.
	Machine string `json:"machine"`
	// IP is the descriptor IP of the request's origin.
	IP string `json:"ip"`
}

// Conflict states a refused attempt to reuse a registration key.
type Conflict struct {
	// UnitType is the contested unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the contested unit identifier within UnitType.
	UnitID uint16 `json:"unit_id"`
	// Existing is the stored pending or accepted request the key already holds.
	Existing Fields `json:"existing"`
	// Attempted is the refused request that reused the key.
	Attempted Fields `json:"attempted"`
	// Reason is the bounded machine-readable conflict code.
	Reason string `json:"reason"`
}

// EventType returns the event's stable dotted kind.
func (Conflict) EventType() events.Type { return TypeConflict }

// Source returns the subsystem that states the fact.
func (Conflict) Source() string { return eventSource }

// Tags marks the conflict as an operational anomaly.
func (Conflict) Tags() []string { return []string{events.TagWarning} }
