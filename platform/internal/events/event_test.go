package events

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

// This package owns the event mechanism and declares no events of its own, so
// its tests use their own doubles. That keeps them honest: the mechanism must
// not know anything about the domains that use it.

// plainEvent is an untagged event.
type plainEvent struct {
	Detail string `json:"detail,omitempty"`
}

func (plainEvent) EventType() Type { return "platform.test.plain" }
func (plainEvent) Source() string  { return "test" }

// taggedEvent declares arbitrary tags, so tag normalization is tested without
// depending on what a real domain happens to tag today.
type taggedEvent struct {
	tags []string
}

func (taggedEvent) EventType() Type  { return "platform.test.tagged" }
func (taggedEvent) Source() string   { return "test" }
func (e taggedEvent) Tags() []string { return e.tags }

// versionedEvent declares its own payload schema version, so the recorder's
// handling of the Versioned interface is tested without a real domain event.
type versionedEvent struct {
	version int
}

func (versionedEvent) EventType() Type      { return "platform.test.versioned" }
func (versionedEvent) Source() string       { return "test" }
func (e versionedEvent) SchemaVersion() int { return e.version }

// testNode is the deployment identity a test recorder stamps onto every record.
var testNode = Node{
	Project:     "scenario",
	Environment: "development",
	Site:        "local",
	Machine:     "node",
	Role:        "all-in-one",
}

func TestNodeFromDescriptorTakesDeploymentIdentity(t *testing.T) {
	node := NodeFromDescriptor(deployment.Descriptor{
		Platform:    "opdl",
		Project:     "scenario",
		Environment: "development",
		Site:        "local",
		Machine:     "node",
		Role:        "all-in-one",
		IP:          "127.0.0.1",
		Services:    []string{"core-services"},
	})

	require.Equal(t, Node{
		Project:     "scenario",
		Environment: "development",
		Site:        "local",
		Machine:     "node",
		Role:        "all-in-one",
	}, node)
}

func TestNormalizeTagsIsDeterministic(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want []string
	}{
		{name: "nil is omitted", tags: nil, want: nil},
		{name: "empty is omitted", tags: []string{}, want: nil},
		{name: "blank tags are dropped", tags: []string{" ", ""}, want: nil},
		{name: "tags are trimmed", tags: []string{"  warning "}, want: []string{"warning"}},
		{name: "duplicates collapse", tags: []string{"warning", "warning"}, want: []string{"warning"}},
		{name: "duplicates collapse after trimming", tags: []string{"warning", " warning"}, want: []string{"warning"}},
		{name: "order is stable", tags: []string{"warning", "audit"}, want: []string{"audit", "warning"}},
		{name: "reverse input gives same order", tags: []string{"audit", "warning"}, want: []string{"audit", "warning"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, normalizeTags(test.tags))
		})
	}
}

func TestEventTagsIgnoresUntaggedEvents(t *testing.T) {
	require.Nil(t, eventTags(plainEvent{}))
	require.Equal(t, []string{"warning"}, eventTags(taggedEvent{tags: []string{"warning"}}))
}

func TestEventSchemaVersionDefaultsWhenUndeclaredOrUnusable(t *testing.T) {
	require.Equal(t, DefaultSchemaVersion, eventSchemaVersion(plainEvent{}))
	require.Equal(t, 3, eventSchemaVersion(versionedEvent{version: 3}))
	require.Equal(t, DefaultSchemaVersion, eventSchemaVersion(versionedEvent{version: 0}),
		"a non-positive declared version falls back to the default")
	require.Equal(t, DefaultSchemaVersion, eventSchemaVersion(versionedEvent{version: -1}))
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
			"role": "all-in-one"
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

// brokenEvent carries a payload encoding/json cannot marshal.
type brokenEvent struct {
	Broken chan int `json:"broken"`
}

func (brokenEvent) EventType() Type { return "platform.test.broken" }
func (brokenEvent) Source() string  { return "test" }
