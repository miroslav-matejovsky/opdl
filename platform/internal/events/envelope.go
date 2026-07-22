package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidEnvelope reports an envelope that is not complete enough to store
// or distribute. Every envelope is validated before it leaves the process, so a
// stored event is always self-describing: a later reader can attribute it, rank
// its severity, version its payload, and decode it without consulting anything
// outside the envelope.
var ErrInvalidEnvelope = errors.New("events: invalid envelope")

// Envelope is the platform's one serialized event wrapper: the common metadata
// plus the typed payload as raw JSON. Local files, the site journal, and any
// later distributor all carry this same shape, so a reader never has to work out
// which writer produced a JSON object before decoding it.
//
// Emitters supply only the payload. Everything else is stamped for them, so no
// business call site handles identity, clocks, or encoding.
//
// The envelope deliberately carries no transport ordering. A shared journal
// orders events when it accepts them, and that sequence belongs to delivery
// metadata, not to the immutable fact.
type Envelope struct {
	// ID is unique per occurrence and sorts in occurrence order.
	ID string `json:"id"`
	// Type is the event kind, in platform.<source>.<fact> form.
	Type Type `json:"type"`
	// SchemaVersion is the version of the payload schema Type encodes. It is
	// positive on every envelope and lets a reader reject an encoding it does
	// not understand instead of guessing at its meaning.
	SchemaVersion int `json:"schema_version"`
	// OccurredAt is when the fact happened, in UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// Source is the subsystem that states the fact, derived from Type.
	Source string `json:"source"`
	// Severity is how much operational attention the fact deserves.
	Severity Severity `json:"severity"`
	// Origin is the deployment and process identity of the writer.
	Origin Origin `json:"origin"`
	// CausationID is the ID of the event whose handling produced this one, empty
	// for an event that begins a chain. It lets a reader follow a reaction back
	// to its cause.
	CausationID string `json:"causation_id,omitempty"`
	// CorrelationID groups every event of one logical workflow, empty when an
	// event starts or belongs to no such workflow. It flows unchanged from a
	// cause onto its consequences.
	CorrelationID string `json:"correlation_id,omitempty"`
	// Tags are optional sorted, duplicate-free markers, omitted when empty.
	Tags []string `json:"tags,omitempty"`
	// StableID is the domain-stable identity of the fact when it has one, empty
	// otherwise. Restating the same fact yields the same StableID, which is what
	// lets an adapter collapse a repeated publication without asking the payload
	// for anything transport-specific.
	StableID string `json:"stable_id,omitempty"`
	// Data is the event payload encoded as JSON.
	Data json.RawMessage `json:"data"`
}

// Validate reports whether e is complete and well formed. It checks what every
// reader depends on: an occurrence identity, a well-formed type whose source
// matches the one stamped, a positive schema version, a UTC occurrence time, a
// known severity, a complete origin, and a decodable payload. Causal links,
// tags, and stable identity are optional and unconstrained when absent.
func (e Envelope) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidEnvelope)
	}
	if err := e.Type.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEnvelope, err)
	}
	if e.Source != e.Type.Source() {
		return fmt.Errorf("%w: source %q does not match type %q", ErrInvalidEnvelope, e.Source, e.Type)
	}
	if e.SchemaVersion <= 0 {
		return fmt.Errorf("%w: schema version must be positive, got %d", ErrInvalidEnvelope, e.SchemaVersion)
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidEnvelope)
	}
	if e.OccurredAt.Location() != time.UTC {
		return fmt.Errorf("%w: occurred_at must be UTC, got %s", ErrInvalidEnvelope, e.OccurredAt.Location())
	}
	if !e.Severity.Valid() {
		return fmt.Errorf("%w: unknown severity %q", ErrInvalidEnvelope, e.Severity)
	}
	if err := e.Origin.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEnvelope, err)
	}
	if len(e.Data) == 0 {
		return fmt.Errorf("%w: payload is required", ErrInvalidEnvelope)
	}
	if !json.Valid(e.Data) {
		return fmt.Errorf("%w: payload is not valid JSON", ErrInvalidEnvelope)
	}
	return nil
}

// Encode renders an envelope as the bytes a writer stores: one compact JSON
// object. It validates first, so an incomplete envelope is a caller error
// reported here rather than an unreadable event written to a journal nothing
// can replay.
func Encode(envelope Envelope) ([]byte, error) {
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("events: encode envelope %s: %w", envelope.Type, err)
	}
	return data, nil
}

// Decode reads a stored envelope. An envelope that does not decode is storage
// corruption, not a bad request, so the error is reported for a reader to stop
// on rather than repaired.
func Decode(data []byte) (Envelope, error) {
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("events: decode envelope: %w", err)
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, fmt.Errorf("events: decode envelope: %w", err)
	}
	return envelope, nil
}
