package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

// These tests cover the temporary previous wrapper. They are deleted with
// legacy.go once every pipeline carries Envelope.

// testNode is the deployment identity a test recorder stamps onto every record.
var testNode = Node{
	Project:        "scenario",
	Environment:    "development",
	Site:           "local",
	Machine:        "node",
	MachineProfile: "all-in-one",
}

func TestNodeFromDescriptorTakesDeploymentIdentity(t *testing.T) {
	node := NodeFromDescriptor(config.Descriptor{
		Platform:       "opdl",
		Project:        "scenario",
		Environment:    "development",
		Site:           "local",
		Machine:        "node",
		MachineProfile: "all-in-one",
		IP:             "127.0.0.1",
		Services:       []string{"core-services"},
	})

	require.Equal(t, Node{
		Project:        "scenario",
		Environment:    "development",
		Site:           "local",
		Machine:        "node",
		MachineProfile: "all-in-one",
	}, node)
}

func TestLegacySchemaVersionDefaultsWhenUndeclaredOrUnusable(t *testing.T) {
	require.Equal(t, DefaultSchemaVersion, legacySchemaVersion(plainEvent{}))
	require.Equal(t, 3, legacySchemaVersion(versionedEvent{version: 3}))
	require.Equal(t, DefaultSchemaVersion, legacySchemaVersion(versionedEvent{version: 0}),
		"a non-positive declared version falls back to the default")
	require.Equal(t, DefaultSchemaVersion, legacySchemaVersion(versionedEvent{version: -1}))
}

func TestStampRecordDerivesSourceFromTheEventType(t *testing.T) {
	record, err := StampRecord(testNode, "id-a", time.Now(), plainEvent{})
	require.NoError(t, err)
	require.Equal(t, "test", record.Source)
	require.Equal(t, testNode, record.Node)
	require.Equal(t, time.UTC, record.OccurredAt.Location())
}

func TestRecordEncodesEnvelopeAndPayloadOnOneLevel(t *testing.T) {
	record, err := newRecord(Meta{
		ID:            "id-a",
		Type:          "platform.test.tagged",
		SchemaVersion: 1,
		Source:        "test",
		Node:          testNode,
		Tags:          []string{"warning"},
	}, plainEvent{Detail: "started"})
	require.NoError(t, err)

	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "id-a",
		"type": "platform.test.tagged",
		"schema_version": 1,
		"occurred_at": "0001-01-01T00:00:00Z",
		"source": "test",
		"node": {
			"project": "scenario",
			"environment": "development",
			"site": "local",
			"machine": "node",
			"machine_profile": "all-in-one"
		},
		"tags": ["warning"],
		"data": {"detail": "started"}
	}`, string(encoded))
}

func TestRecordOmitsUnsetCausalLinks(t *testing.T) {
	record, err := newRecord(Meta{ID: "id-a", Type: "platform.test.plain", SchemaVersion: 1, Node: testNode}, plainEvent{})
	require.NoError(t, err)

	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "causation_id")
	require.NotContains(t, string(encoded), "correlation_id")
}

func TestRecordKeepsCausalLinksWhenSet(t *testing.T) {
	record, err := newRecord(Meta{
		ID:            "id-b",
		Type:          "platform.test.plain",
		SchemaVersion: 1,
		Node:          testNode,
		CausationID:   "id-a",
		CorrelationID: "workflow-1",
	}, plainEvent{})
	require.NoError(t, err)

	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"causation_id":"id-a"`)
	require.Contains(t, string(encoded), `"correlation_id":"workflow-1"`)
}

func TestNewRecordReportsUnencodablePayload(t *testing.T) {
	_, err := newRecord(Meta{Type: "platform.test.broken"}, brokenEvent{})
	require.ErrorContains(t, err, "encode platform.test.broken payload")
}
