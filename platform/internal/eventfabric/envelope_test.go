package eventfabric

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

// validRecord is a complete, well-formed record. Each validation test starts
// from it and breaks exactly one field, so a failure names the rule it broke.
func validRecord() events.Record {
	return events.Record{
		Meta: events.Meta{
			ID:            "id-a",
			Type:          "platform.registration.accepted",
			SchemaVersion: 1,
			OccurredAt:    time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
			Source:        "registration",
			Node: events.Node{
				Project:        "customer-a",
				Environment:    "production",
				Site:           "north",
				Machine:        "node-a",
				MachineProfile: "all-in-one",
			},
		},
		Data: json.RawMessage(`{"unit_type":7,"unit_id":42}`),
	}
}

func TestValidateEnvelopeAcceptsACompleteRecord(t *testing.T) {
	require.NoError(t, ValidateEnvelope(validRecord()))
}

func TestValidateEnvelopeAcceptsOptionalCausalLinks(t *testing.T) {
	record := validRecord()
	record.CausationID = "id-cause"
	record.CorrelationID = "workflow-1"
	require.NoError(t, ValidateEnvelope(record))
}

func TestValidateEnvelopeRejectsIncompleteRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*events.Record)
	}{
		{name: "blank id", mutate: func(r *events.Record) { r.ID = "" }},
		{name: "unroutable type", mutate: func(r *events.Record) { r.Type = "registration.accepted" }},
		{name: "blank type", mutate: func(r *events.Record) { r.Type = "" }},
		{name: "zero schema version", mutate: func(r *events.Record) { r.SchemaVersion = 0 }},
		{name: "negative schema version", mutate: func(r *events.Record) { r.SchemaVersion = -1 }},
		{name: "zero occurred_at", mutate: func(r *events.Record) { r.OccurredAt = time.Time{} }},
		{name: "blank source", mutate: func(r *events.Record) { r.Source = "" }},
		{name: "missing node project", mutate: func(r *events.Record) { r.Node.Project = "" }},
		{name: "missing node environment", mutate: func(r *events.Record) { r.Node.Environment = "" }},
		{name: "missing node site", mutate: func(r *events.Record) { r.Node.Site = "" }},
		{name: "missing node machine", mutate: func(r *events.Record) { r.Node.Machine = "" }},
		{name: "missing node role", mutate: func(r *events.Record) { r.Node.MachineProfile = "" }},
		{name: "empty payload", mutate: func(r *events.Record) { r.Data = nil }},
		{name: "malformed payload", mutate: func(r *events.Record) { r.Data = json.RawMessage(`{invalid`) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validRecord()
			test.mutate(&record)
			require.ErrorIs(t, ValidateEnvelope(record), ErrInvalidEnvelope)
		})
	}
}

func TestEncodeRejectsAnInvalidRecordInsteadOfWritingIt(t *testing.T) {
	record := validRecord()
	record.ID = ""
	_, err := Encode(record)
	require.ErrorIs(t, err, ErrInvalidEnvelope)
}

func TestEncodeThenDecodeRoundTripsARecord(t *testing.T) {
	original := validRecord()
	original.CausationID = "id-cause"

	encoded, err := Encode(original)
	require.NoError(t, err)

	decoded, err := Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, original.Meta, decoded.Meta)
	require.JSONEq(t, string(original.Data), string(decoded.Data))
}

func TestDecodeReportsCorruptBytes(t *testing.T) {
	_, err := Decode([]byte(`{not json`))
	require.ErrorContains(t, err, "decode record")
}
