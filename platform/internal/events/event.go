package events

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrInvalidEvent reports an event that declares metadata nothing can use, such
// as an unknown severity or a blank stable identity. An event declares its
// metadata in code, so this is always a programming error surfaced at the point
// the envelope is stamped.
var ErrInvalidEvent = errors.New("events: invalid event")

// TagWarning marks an event that states a rejected or conflicting operation. It
// lets a reader select the operational anomalies without knowing every event a
// package can produce.
const TagWarning = "warning"

// DefaultSchemaVersion is the payload schema version stamped on an event that
// does not declare its own. Every event carries a positive schema version so a
// reader that replays a journal can refuse a payload it does not understand
// instead of guessing at its meaning.
const DefaultSchemaVersion = 1

// Event is one domain fact: a small immutable struct of payload fields that
// knows its own kind. A package declares the events it owns in its own
// events.go; this package declares none.
//
// A payload carries what a reader needs to understand the fact without querying
// the platform, and nothing a reader must not see. Its JSON encoding is a
// published contract.
//
// The interface is deliberately one method. Everything else on the envelope is
// either derived from the type or defaulted, so a normal informational event is
// declared in full by:
//
//	func (Accepted) EventType() events.Type { return TypeAccepted }
//
// An event that differs from a default says so by implementing one of the
// optional interfaces below, and nothing more.
type Event interface {
	// EventType returns the event's stable dotted kind.
	EventType() Type
}

// Versioned is the optional interface an Event implements to declare the schema
// version of its payload. An event that does not implement it records
// DefaultSchemaVersion. An event that evolves its payload increments this so a
// reader can tell one encoding from another.
type Versioned interface {
	// SchemaVersion returns the positive version of the event's payload schema.
	SchemaVersion() int
}

// Severe is the optional interface an Event implements when the fact deserves
// more than routine attention. An event that does not implement it records
// DefaultSeverity.
type Severe interface {
	// Severity returns how much operational attention the fact deserves.
	Severity() Severity
}

// Tagged is the optional interface an Event implements when it carries tags,
// typically TagWarning. Stamping normalizes whatever it returns; an event that
// does not implement Tagged records no tags.
type Tagged interface {
	// Tags returns the markers to stamp onto the event, in any order.
	Tags() []string
}

// Identified is the optional interface an Event implements when the fact has a
// domain-stable identity: a value derived from the fact itself, so restating
// the same fact yields the same identity.
//
// It is domain vocabulary on purpose. A writer that collapses a repeated
// publication may use this identity to do so, but the event never knows that;
// it only states what makes the fact the same fact.
type Identified interface {
	// StableID returns the fact's non-empty domain-stable identity.
	StableID() string
}

// schemaVersionOf returns the payload schema version event declares, or
// DefaultSchemaVersion when it declares none. A non-positive declared version
// is rejected rather than corrected: a reader trusts the version to decide how
// to decode, so an unusable one must not reach the journal.
func schemaVersionOf(event Event) (int, error) {
	versioned, ok := event.(Versioned)
	if !ok {
		return DefaultSchemaVersion, nil
	}
	version := versioned.SchemaVersion()
	if version <= 0 {
		return 0, fmt.Errorf("%w: %s declares schema version %d, want a positive version", ErrInvalidEvent, event.EventType(), version)
	}
	return version, nil
}

// severityOf returns the severity event declares, or DefaultSeverity when it
// declares none. An unknown severity is rejected: operators filter on the
// closed set, so a fourth level would silently drop out of every view.
func severityOf(event Event) (Severity, error) {
	severe, ok := event.(Severe)
	if !ok {
		return DefaultSeverity, nil
	}
	severity := severe.Severity()
	if !severity.Valid() {
		return "", fmt.Errorf("%w: %s declares unknown severity %q", ErrInvalidEvent, event.EventType(), severity)
	}
	return severity, nil
}

// tagsOf returns the normalized tags event declares, or nil when it declares
// none.
func tagsOf(event Event) []string {
	tagged, ok := event.(Tagged)
	if !ok {
		return nil
	}
	return normalizeTags(tagged.Tags())
}

// stableIDOf returns the stable identity event declares, or an empty string
// when it declares none. An event that implements Identified must produce an
// identity: a blank one is not "no identity", it is a broken derivation that
// would make every occurrence look like the same fact.
func stableIDOf(event Event) (string, error) {
	identified, ok := event.(Identified)
	if !ok {
		return "", nil
	}
	stableID := strings.TrimSpace(identified.StableID())
	if stableID == "" {
		return "", fmt.Errorf("%w: %s declares an empty stable identity", ErrInvalidEvent, event.EventType())
	}
	return stableID, nil
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
