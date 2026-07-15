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

func TestRecordEncodesEnvelopeAndPayloadOnOneLevel(t *testing.T) {
	record, err := newRecord(Meta{
		ID:       "id-a",
		Type:     "platform.test.tagged",
		Sequence: 1,
		Source:   "test",
		Tags:     []string{"warning"},
	}, plainEvent{Detail: "started"})
	require.NoError(t, err)

	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "id-a",
		"type": "platform.test.tagged",
		"sequence": 1,
		"occurred_at": "0001-01-01T00:00:00Z",
		"source": "test",
		"tags": ["warning"],
		"data": {"detail": "started"}
	}`, string(encoded))
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
