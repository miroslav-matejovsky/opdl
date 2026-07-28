package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

// ErrUncomposedFactory reports a zero Factory. A factory is composed once by
// the runtime that owns the process identity, so a zero one means an envelope
// was stamped by something that never learned which process it is.
var ErrUncomposedFactory = errors.New("events: factory was not composed with NewFactory")

// Factory stamps a typed payload with everything an envelope needs that the
// payload does not know: occurrence identity and time, the process that states
// the fact, the metadata defaults, and the causal links of the work in
// progress.
//
// It is the platform's one stamper and is composed once per process, from the
// deployment descriptor the platform booted with. That is what makes origin
// unforgeable: an emitter cannot claim to be another machine, because it never
// supplies identity at all. Business code passes a payload and a context, and
// nothing else.
//
// A Factory is immutable after construction and safe for concurrent use.
type Factory struct {
	// origin is this process's identity, fixed for the whole run.
	origin Origin
	// now reads the wall clock, and newID mints occurrence identities. Both are
	// fields rather than direct calls so package tests can stamp deterministic
	// envelopes; no caller outside this package can replace them.
	now   func() time.Time
	newID func() (string, error)
}

// NewFactory composes the stamper for this process from the deployment
// descriptor and the local role of the running process, such as primary or
// standby. It reads the process identifier itself, so no caller can state one.
//
// It fails when the descriptor does not name a complete deployment. That is a
// composition error, caught once at start rather than on every event the
// process would fail to stamp.
func NewFactory(descriptor config.Descriptor, processRole string) (Factory, error) {
	origin := Origin{
		Machine:        descriptor.Machine,
		MachineProfile: descriptor.MachineProfile,
		ProcessRole:    processRole,
		PID:            os.Getpid(),
	}
	if err := origin.Validate(); err != nil {
		return Factory{}, fmt.Errorf("events: compose factory: %w", err)
	}
	return Factory{origin: origin, now: time.Now, newID: newEventID}, nil
}

// Origin returns the identity every envelope this factory stamps carries. It is
// the same for the life of the process.
func (f Factory) Origin() Origin { return f.origin }

// Wrap turns a typed payload into a complete, validated envelope: a fresh
// occurrence identity and UTC time, the event's type and derived source, its
// declared metadata or the defaults, this process's origin, the causal links
// ctx carries, and the payload encoded as JSON.
//
// It reports an error and stamps nothing when the event declares metadata
// nothing can use, when the payload does not encode, or when the completed
// envelope is not valid. It does not log: the caller decides whether a fact it
// cannot state is fatal or best effort.
func (f Factory) Wrap(ctx context.Context, event Event) (Envelope, error) {
	if f.now == nil || f.newID == nil {
		return Envelope{}, ErrUncomposedFactory
	}
	eventType := event.EventType()
	if err := eventType.Validate(); err != nil {
		return Envelope{}, err
	}
	schemaVersion, err := schemaVersionOf(event)
	if err != nil {
		return Envelope{}, err
	}
	severity, err := severityOf(event)
	if err != nil {
		return Envelope{}, err
	}
	stableID, err := stableIDOf(event)
	if err != nil {
		return Envelope{}, err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return Envelope{}, fmt.Errorf("events: encode %s payload: %w", eventType, err)
	}
	id, err := f.newID()
	if err != nil {
		return Envelope{}, err
	}
	causationID, correlationID := causalLinks(ctx)

	envelope := Envelope{
		ID:            id,
		Type:          eventType,
		SchemaVersion: schemaVersion,
		OccurredAt:    f.now().UTC(),
		Source:        eventType.Source(),
		Severity:      severity,
		Origin:        f.origin,
		CausationID:   causationID,
		CorrelationID: correlationID,
		Tags:          tagsOf(event),
		StableID:      stableID,
		Data:          data,
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, fmt.Errorf("events: wrap %s: %w", eventType, err)
	}
	return envelope, nil
}
