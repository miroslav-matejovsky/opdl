package eventfabric

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// ErrInvalidEnvelope reports a record whose envelope is not complete enough to
// publish or replay. The Event Fabric validates every record before it reaches
// the journal, so a stored event is always self-describing: a later reader can
// attribute it, order its schema, and decode its payload without consulting
// anything outside the record.
var ErrInvalidEnvelope = errors.New("eventfabric: invalid event envelope")

// ValidateEnvelope reports whether record is complete and well formed. It checks
// the envelope a shared journal depends on: a unique identity, a routable type,
// a positive schema version, an occurrence time, an emitting source, a complete
// deployment node, and a non-empty JSON payload. Causal links are optional and
// unconstrained when absent.
func ValidateEnvelope(record events.Record) error {
	if record.ID == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidEnvelope)
	}
	if _, err := ParseEventType(record.Type); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEnvelope, err)
	}
	if record.SchemaVersion <= 0 {
		return fmt.Errorf("%w: schema version must be positive, got %d", ErrInvalidEnvelope, record.SchemaVersion)
	}
	if record.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidEnvelope)
	}
	if record.Source == "" {
		return fmt.Errorf("%w: source is required", ErrInvalidEnvelope)
	}
	if err := validateNode(record.Node); err != nil {
		return err
	}
	if len(record.Data) == 0 {
		return fmt.Errorf("%w: payload is required", ErrInvalidEnvelope)
	}
	if !json.Valid(record.Data) {
		return fmt.Errorf("%w: payload is not valid JSON", ErrInvalidEnvelope)
	}
	return nil
}

// validateNode reports whether node names a complete deployment identity. Every
// field is required: a shared journal that pools nodes cannot attribute an event
// whose origin is only partly stated.
func validateNode(node events.Node) error {
	missing := ""
	switch {
	case node.Project == "":
		missing = "project"
	case node.Environment == "":
		missing = "environment"
	case node.Site == "":
		missing = "site"
	case node.Machine == "":
		missing = "machine"
	case node.MachineProfile == "":
		missing = "machine_profile"
	}
	if missing != "" {
		return fmt.Errorf("%w: node %s is required", ErrInvalidEnvelope, missing)
	}
	return nil
}

// Encode renders a validated record as the bytes the journal stores: one compact
// JSON object. It validates first, so an invalid record is a caller error
// reported here rather than an unreadable event written to the journal.
func Encode(record events.Record) ([]byte, error) {
	if err := ValidateEnvelope(record); err != nil {
		return nil, err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("eventfabric: encode record %s: %w", record.Type, err)
	}
	return data, nil
}

// Decode reads a record the journal returned. A record that does not decode is
// journal corruption, not a bad request, so the error is reported for a reader
// to stop on rather than repaired.
func Decode(data []byte) (events.Record, error) {
	var record events.Record
	if err := json.Unmarshal(data, &record); err != nil {
		return events.Record{}, fmt.Errorf("eventfabric: decode record: %w", err)
	}
	return record, nil
}
