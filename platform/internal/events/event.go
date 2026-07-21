package events

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

// TagWarning marks an event that states a rejected or conflicting operation. It
// lets a reader select the operational anomalies without knowing every event a
// package can produce.
const TagWarning = "warning"

// Type is the stable, dotted identifier of an event kind, e.g.
// "platform.registration.accepted". It is the discriminator stored with every
// record and the key readers filter on. Types read as facts:
// platform.<area>.<fact>, past tense.
type Type string

// Node is the identity of the platform process an event is about: which machine
// of which deployment stated the fact.
//
// It is constant for a whole process run and is stamped onto every record. The
// shared site journal pools events from several nodes, so identity must travel
// with each fact.
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
func NodeFromDescriptor(d deployment.Descriptor) Node {
	return Node{
		Project:        d.Project,
		Environment:    d.Environment,
		Site:           d.Site,
		Machine:        d.Machine,
		MachineProfile: d.MachineProfile,
	}
}

// DefaultSchemaVersion is the payload schema version stamped on an event that
// does not declare its own. Every event carries a positive schema version so a
// reader that replays a journal can refuse a payload it does not understand
// instead of guessing at its meaning.
const DefaultSchemaVersion = 1

// Meta is the envelope every event carries. The Event Fabric publisher sets it;
// emitters never populate it themselves.
//
// The envelope is self-describing: it identifies the occurrence, names the
// deployment node that stated the fact, and versions the payload schema. It
// deliberately carries no transport ordering. A shared journal orders events
// when it accepts them, and that sequence belongs to the transport receipt and
// delivery, not to the immutable fact. See eventfabric.Receipt and
// eventfabric.Delivery.
type Meta struct {
	// ID is unique per occurrence and sorts in occurrence order.
	ID string `json:"id"`
	// Type is the event kind.
	Type Type `json:"type"`
	// SchemaVersion is the version of the payload schema Type encodes. It is
	// positive on every record and lets a reader reject an unknown encoding.
	SchemaVersion int `json:"schema_version"`
	// OccurredAt is when the fact happened, in UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// Source is the emitting subsystem.
	Source string `json:"source"`
	// Node is the deployment identity of the process that stated the fact. It is
	// on every record, not only in a per-node file name, so a shared journal that
	// pools every node's events keeps each fact attributed to its origin.
	Node Node `json:"node"`
	// CausationID is the ID of the event whose handling produced this one, empty
	// for an event that begins a chain. It lets a reader follow a reaction back
	// to its cause. The Event Fabric sets it from the delivery a handler is
	// processing; the plain recorder leaves it empty.
	CausationID string `json:"causation_id,omitempty"`
	// CorrelationID groups every event of one logical workflow, empty when a
	// record starts or belongs to no such workflow. It flows unchanged from a
	// cause onto its consequences.
	CorrelationID string `json:"correlation_id,omitempty"`
	// Tags are optional sorted, duplicate-free markers, omitted when empty.
	Tags []string `json:"tags,omitempty"`
}

// Event is one domain fact: a small immutable struct of payload fields that
// knows its own kind and the subsystem that states it. A package declares the
// events it owns in its own events.go; this package declares none.
//
// A payload carries what a reader needs to understand the fact without querying
// the platform, and nothing a reader must not see. Its JSON encoding is a
// published contract.
type Event interface {
	// EventType returns the event's stable dotted kind.
	EventType() Type
	// Source returns the subsystem that states the fact.
	Source() string
}

// Tagged is the optional interface an Event implements when it carries tags,
// typically TagWarning. Envelope stamping normalizes whatever it returns; an
// event that does not implement Tagged records no tags.
type Tagged interface {
	// Tags returns the markers to stamp onto the event, in any order.
	Tags() []string
}

// Versioned is the optional interface an Event implements to declare the schema
// version of its payload. An event that does not implement it records
// DefaultSchemaVersion. An event that evolves its payload increments this so a
// reader can tell one encoding from another.
type Versioned interface {
	// SchemaVersion returns the positive version of the event's payload schema.
	SchemaVersion() int
}

// Record is the stored form of an event: the envelope plus the payload as raw
// JSON. Meta is embedded, so its fields sit at the top level next to data.
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
// id and node. It is the one place an event's envelope is constructed for the
// Event Fabric publisher. It does not set the causal links; a publisher that reacts
// to a delivery sets those from the delivery it is handling.
func StampRecord(node Node, id string, occurredAt time.Time, event Event) (Record, error) {
	return newRecord(Meta{
		ID:            id,
		Type:          event.EventType(),
		SchemaVersion: eventSchemaVersion(event),
		OccurredAt:    occurredAt.UTC(),
		Source:        event.Source(),
		Node:          node,
		Tags:          eventTags(event),
	}, event)
}

// eventTags returns the normalized tags event declares, or nil when it declares
// none.
func eventTags(event Event) []string {
	tagged, ok := event.(Tagged)
	if !ok {
		return nil
	}
	return normalizeTags(tagged.Tags())
}

// eventSchemaVersion returns the payload schema version event declares, or
// DefaultSchemaVersion when it declares none. A non-positive declared version is
// treated as the default: a version is metadata a reader trusts, so an
// unusable one is corrected rather than stored.
func eventSchemaVersion(event Event) int {
	versioned, ok := event.(Versioned)
	if !ok {
		return DefaultSchemaVersion
	}
	if version := versioned.SchemaVersion(); version > 0 {
		return version
	}
	return DefaultSchemaVersion
}

// normalizeTags returns tags trimmed, deduplicated, and sorted, so the same set
// always encodes identically. Blank tags are dropped, and an empty result is
// nil so the envelope omits the field entirely.
func normalizeTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || slices.Contains(normalized, tag) {
			continue
		}
		normalized = append(normalized, tag)
	}
	if len(normalized) == 0 {
		return nil
	}
	slices.Sort(normalized)
	return normalized
}
