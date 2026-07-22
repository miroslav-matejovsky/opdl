package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

// This file is temporary scaffolding, not a compatibility surface.
//
// It holds the previous event wrapper — Node, Meta, Record, and StampRecord —
// while the platform is migrated onto Envelope one pipeline at a time. Nothing
// new may be built on it, no adapter may keep reading it, and the whole file is
// deleted once the journal, the local recorder, and the NATS adapter carry
// Envelope instead. See docs/plan/unified-events.
//
// The one change made here is that Source is now derived from the event type
// rather than asked of the event, because the canonical Event interface has a
// single method. The stored JSON is unchanged: every existing event's declared
// source is already its type's middle token.

// NewID returns a time-ordered unique event ID. Factory mints an occurrence
// identity for every envelope it stamps, so this exists only for the pipelines
// that still build a Record by hand.
func NewID() string { return newEventID() }

// Node is the identity of the platform process an event is about: which machine
// of which deployment stated the fact. Envelope.Origin replaces it and also
// carries process identity.
type Node struct {
	// Project is the deployment project identifier.
	Project string `json:"project"`
	// Environment is the deployment environment name.
	Environment string `json:"environment"`
	// Site is the deployment site identifier.
	Site string `json:"site"`
	// Machine is the deployment machine identifier.
	Machine string `json:"machine"`
	// MachineProfile is the machine's purpose, such as "sensor-node".
	MachineProfile string `json:"machine_profile"`
}

// NodeFromDescriptor derives the node identity from the deployment descriptor
// the platform booted with. The descriptor is the platform's own compiled-in
// identity, so an event's node is never something a caller can claim to be.
func NodeFromDescriptor(d config.Descriptor) Node {
	return Node{
		Project:        d.Project,
		Environment:    d.Environment,
		Site:           d.Site,
		Machine:        d.Machine,
		MachineProfile: d.MachineProfile,
	}
}

// Meta is the previous envelope, superseded by Envelope.
type Meta struct {
	// ID is unique per occurrence and sorts in occurrence order.
	ID string `json:"id"`
	// Type is the event kind.
	Type Type `json:"type"`
	// SchemaVersion is the version of the payload schema Type encodes.
	SchemaVersion int `json:"schema_version"`
	// OccurredAt is when the fact happened, in UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// Source is the emitting subsystem.
	Source string `json:"source"`
	// Node is the deployment identity of the process that stated the fact.
	Node Node `json:"node"`
	// CausationID is the ID of the event whose handling produced this one, empty
	// for an event that begins a chain.
	CausationID string `json:"causation_id,omitempty"`
	// CorrelationID groups every event of one logical workflow, empty when a
	// record starts or belongs to no such workflow.
	CorrelationID string `json:"correlation_id,omitempty"`
	// Tags are optional sorted, duplicate-free markers, omitted when empty.
	Tags []string `json:"tags,omitempty"`
}

// Record is the previous stored form of an event: the envelope plus the payload
// as raw JSON. Envelope replaces both halves.
type Record struct {
	Meta
	// Data is the event payload encoded as JSON.
	Data json.RawMessage `json:"data"`
}

// newRecord encodes event's payload and wraps it with meta.
func newRecord(meta Meta, event Event) (Record, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return Record{}, fmt.Errorf("encode %s payload: %w", meta.Type, err)
	}
	return Record{Meta: meta, Data: data}, nil
}

// StampRecord builds the stored record for event as of occurredAt, stamped with
// id and node. It does not set the causal links; a publisher that reacts to a
// delivery sets those from the delivery it is handling.
func StampRecord(node Node, id string, occurredAt time.Time, event Event) (Record, error) {
	return newRecord(Meta{
		ID:            id,
		Type:          event.EventType(),
		SchemaVersion: legacySchemaVersion(event),
		OccurredAt:    occurredAt.UTC(),
		Source:        event.EventType().Source(),
		Node:          node,
		Tags:          tagsOf(event),
	}, event)
}

// legacySchemaVersion returns the payload schema version event declares, or
// DefaultSchemaVersion when it declares none or declares an unusable one. The
// canonical model rejects an unusable version instead; this path keeps the old
// lenient behavior because StampRecord's callers are migrated, not changed.
func legacySchemaVersion(event Event) int {
	versioned, ok := event.(Versioned)
	if !ok {
		return DefaultSchemaVersion
	}
	if version := versioned.SchemaVersion(); version > 0 {
		return version
	}
	return DefaultSchemaVersion
}
