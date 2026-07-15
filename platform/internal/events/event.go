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
// It is constant for a whole process run, so it is not part of the envelope. A
// sink states it once for everything it stores, which is why Node is passed to
// a sink rather than to the Recorder. A future sink that pools events from
// several nodes must store it per record.
type Node struct {
	// Project is the deployment project identifier.
	Project string `json:"project"`
	// Environment is the deployment environment name.
	Environment string `json:"environment"`
	// Site is the deployment site identifier.
	Site string `json:"site"`
	// Machine is the deployment machine identifier.
	Machine string `json:"machine"`
	// Role is the machine's role.
	Role string `json:"role"`
}

// NodeFromDescriptor derives the node identity from the deployment descriptor
// the platform booted with. The descriptor is the platform's own compiled-in
// identity, so an event's node is never something a caller can claim to be.
func NodeFromDescriptor(d deployment.Descriptor) Node {
	return Node{
		Project:     d.Project,
		Environment: d.Environment,
		Site:        d.Site,
		Machine:     d.Machine,
		Role:        d.Role,
	}
}

// Meta is the envelope every event carries. The Recorder sets it; emitters
// never populate it themselves.
type Meta struct {
	// ID is unique per occurrence and sorts in occurrence order.
	ID string `json:"id"`
	// Type is the event kind.
	Type Type `json:"type"`
	// Sequence is a monotonic counter within one process run. It restarts on
	// every run, by design, and orders events recorded within one timestamp.
	Sequence uint64 `json:"sequence"`
	// OccurredAt is when the fact happened, in UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// Source is the emitting subsystem.
	Source string `json:"source"`
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
// typically TagWarning. The Recorder normalizes whatever it returns; an event
// that does not implement Tagged records no tags.
type Tagged interface {
	// Tags returns the markers to stamp onto the event, in any order.
	Tags() []string
}

// Record is the stored form of an event: the envelope plus the payload as raw
// JSON. It marshals to exactly one line in a JSONL sink. Meta is embedded, so
// its fields sit at the top level next to data.
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

// eventTags returns the normalized tags event declares, or nil when it declares
// none.
func eventTags(event Event) []string {
	tagged, ok := event.(Tagged)
	if !ok {
		return nil
	}
	return normalizeTags(tagged.Tags())
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
