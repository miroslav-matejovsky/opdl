package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testOrigin is the complete process identity a test envelope carries.
var testOrigin = Origin{
	Project:        "scenario",
	Environment:    "development",
	Site:           "local",
	Machine:        "node",
	MachineProfile: "all-in-one",
	ProcessRole:    "primary",
	PID:            4242,
}

// testOccurredAt is a fixed UTC occurrence time, so encoding is deterministic.
var testOccurredAt = time.Date(2026, time.July, 22, 10, 30, 0, 0, time.UTC)

// validEnvelope returns a complete envelope every validation case starts from.
func validEnvelope() Envelope {
	return Envelope{
		ID:            "id-a",
		Type:          "platform.test.plain",
		SchemaVersion: DefaultSchemaVersion,
		OccurredAt:    testOccurredAt,
		Source:        "test",
		Severity:      DefaultSeverity,
		Origin:        testOrigin,
		Data:          json.RawMessage(`{"detail":"started"}`),
	}
}

func TestEnvelopeValidateAcceptsACompleteEnvelope(t *testing.T) {
	require.NoError(t, validEnvelope().Validate())

	full := validEnvelope()
	full.Severity = SeverityWarn
	full.CausationID = "id-cause"
	full.CorrelationID = "workflow-1"
	full.Tags = []string{TagWarning}
	full.StableID = "proposal-1"
	require.NoError(t, full.Validate())
}

func TestEnvelopeValidateRejectsIncompleteEnvelopes(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Envelope)
		wantErr string
	}{
		{name: "missing id", mutate: func(e *Envelope) { e.ID = "" }, wantErr: "id is required"},
		{name: "malformed type", mutate: func(e *Envelope) { e.Type = "test.plain" }, wantErr: "exactly three tokens"},
		{name: "blank type", mutate: func(e *Envelope) { e.Type = "" }, wantErr: "exactly three tokens"},
		{
			name:    "source disagrees with type",
			mutate:  func(e *Envelope) { e.Source = "other" },
			wantErr: `source "other" does not match type`,
		},
		{name: "zero schema version", mutate: func(e *Envelope) { e.SchemaVersion = 0 }, wantErr: "schema version must be positive"},
		{name: "negative schema version", mutate: func(e *Envelope) { e.SchemaVersion = -1 }, wantErr: "schema version must be positive"},
		{name: "zero occurrence time", mutate: func(e *Envelope) { e.OccurredAt = time.Time{} }, wantErr: "occurred_at is required"},
		{
			name:    "non-UTC occurrence time",
			mutate:  func(e *Envelope) { e.OccurredAt = testOccurredAt.In(time.FixedZone("CET", 3600)) },
			wantErr: "occurred_at must be UTC",
		},
		{name: "missing severity", mutate: func(e *Envelope) { e.Severity = "" }, wantErr: "unknown severity"},
		{name: "unknown severity", mutate: func(e *Envelope) { e.Severity = "fatal" }, wantErr: "unknown severity"},
		{name: "missing origin project", mutate: func(e *Envelope) { e.Origin.Project = "" }, wantErr: "origin project is required"},
		{
			name:    "missing origin environment",
			mutate:  func(e *Envelope) { e.Origin.Environment = "" },
			wantErr: "origin environment is required",
		},
		{name: "missing origin site", mutate: func(e *Envelope) { e.Origin.Site = "" }, wantErr: "origin site is required"},
		{name: "missing origin machine", mutate: func(e *Envelope) { e.Origin.Machine = "" }, wantErr: "origin machine is required"},
		{
			name:    "missing origin machine profile",
			mutate:  func(e *Envelope) { e.Origin.MachineProfile = "" },
			wantErr: "origin machine_profile is required",
		},
		{
			name:    "missing origin process role",
			mutate:  func(e *Envelope) { e.Origin.ProcessRole = "" },
			wantErr: "origin process_role is required",
		},
		{name: "missing origin pid", mutate: func(e *Envelope) { e.Origin.PID = 0 }, wantErr: "origin pid must be positive"},
		{name: "missing payload", mutate: func(e *Envelope) { e.Data = nil }, wantErr: "payload is required"},
		{name: "empty payload", mutate: func(e *Envelope) { e.Data = json.RawMessage{} }, wantErr: "payload is required"},
		{
			name:    "malformed payload",
			mutate:  func(e *Envelope) { e.Data = json.RawMessage(`{invalid`) },
			wantErr: "payload is not valid JSON",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := validEnvelope()
			test.mutate(&envelope)

			err := envelope.Validate()
			require.ErrorIs(t, err, ErrInvalidEnvelope)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestEnvelopeEncodesMetadataAndPayloadOnOneLevel(t *testing.T) {
	envelope := validEnvelope()
	envelope.Severity = SeverityWarn
	envelope.Tags = []string{TagWarning}
	envelope.StableID = "proposal-1"

	encoded, err := json.Marshal(envelope)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "id-a",
		"type": "platform.test.plain",
		"schema_version": 1,
		"occurred_at": "2026-07-22T10:30:00Z",
		"source": "test",
		"severity": "warn",
		"origin": {
			"project": "scenario",
			"environment": "development",
			"site": "local",
			"machine": "node",
			"machine_profile": "all-in-one",
			"process_role": "primary",
			"pid": 4242
		},
		"tags": ["warning"],
		"stable_id": "proposal-1",
		"data": {"detail": "started"}
	}`, string(encoded))
}

func TestEnvelopeOmitsUnsetOptionalMetadata(t *testing.T) {
	encoded, err := json.Marshal(validEnvelope())
	require.NoError(t, err)

	for _, field := range []string{"causation_id", "correlation_id", "tags", "stable_id"} {
		require.NotContains(t, string(encoded), field)
	}
}

func TestEnvelopeSurvivesAJSONRoundTrip(t *testing.T) {
	envelope := validEnvelope()
	envelope.CausationID = "id-cause"
	envelope.CorrelationID = "workflow-1"
	envelope.Tags = []string{"audit", TagWarning}
	envelope.StableID = "proposal-1"

	encoded, err := json.Marshal(envelope)
	require.NoError(t, err)

	var decoded Envelope
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.NoError(t, decoded.Validate())
	require.True(t, envelope.OccurredAt.Equal(decoded.OccurredAt))
	require.Equal(t, time.UTC, decoded.OccurredAt.Location(), "a decoded occurrence time stays UTC")

	decoded.OccurredAt = envelope.OccurredAt
	require.Equal(t, envelope, decoded)
}
