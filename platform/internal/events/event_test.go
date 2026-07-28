package events

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// This package owns the event mechanism and declares no events of its own, so
// its tests use their own doubles. That keeps them honest: the mechanism must
// not know anything about the domains that use it.

// plainEvent implements nothing but Event, so it exercises every default.
type plainEvent struct {
	Detail string `json:"detail,omitempty"`
}

func (plainEvent) EventType() Type { return "platform.test.plain" }

// taggedEvent declares arbitrary tags, so tag normalization is tested without
// depending on what a real domain happens to tag today.
type taggedEvent struct {
	tags []string
}

func (taggedEvent) EventType() Type  { return "platform.test.tagged" }
func (e taggedEvent) Tags() []string { return e.tags }

// versionedEvent declares its own payload schema version.
type versionedEvent struct {
	version int
}

func (versionedEvent) EventType() Type      { return "platform.test.versioned" }
func (e versionedEvent) SchemaVersion() int { return e.version }

// severeEvent declares its own severity.
type severeEvent struct {
	severity Severity
}

func (severeEvent) EventType() Type      { return "platform.test.severe" }
func (e severeEvent) Severity() Severity { return e.severity }

// identifiedEvent declares a domain-stable identity.
type identifiedEvent struct {
	stableID string
}

func (identifiedEvent) EventType() Type    { return "platform.test.identified" }
func (e identifiedEvent) StableID() string { return e.stableID }

// brokenEvent carries a payload encoding/json cannot marshal.
type brokenEvent struct {
	Broken chan int `json:"broken"`
}

func (brokenEvent) EventType() Type { return "platform.test.broken" }

func TestSchemaVersionDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name    string
		event   Event
		want    int
		wantErr string
	}{
		{name: "undeclared defaults", event: plainEvent{}, want: DefaultSchemaVersion},
		{name: "declared wins", event: versionedEvent{version: 3}, want: 3},
		{name: "zero is rejected", event: versionedEvent{version: 0}, wantErr: "declares schema version 0"},
		{name: "negative is rejected", event: versionedEvent{version: -1}, wantErr: "declares schema version -1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, err := schemaVersionOf(test.event)
			if test.wantErr != "" {
				require.ErrorIs(t, err, ErrInvalidEvent)
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, version)
		})
	}
}

func TestSeverityDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name    string
		event   Event
		want    Severity
		wantErr string
	}{
		{name: "undeclared defaults to info", event: plainEvent{}, want: SeverityInfo},
		{name: "declared warn wins", event: severeEvent{severity: SeverityWarn}, want: SeverityWarn},
		{name: "declared error wins", event: severeEvent{severity: SeverityError}, want: SeverityError},
		{name: "unknown is rejected", event: severeEvent{severity: "fatal"}, wantErr: `declares unknown severity "fatal"`},
		{name: "empty is rejected", event: severeEvent{}, wantErr: `declares unknown severity ""`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			severity, err := severityOf(test.event)
			if test.wantErr != "" {
				require.ErrorIs(t, err, ErrInvalidEvent)
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, severity)
		})
	}
}

func TestStableIDIsAbsentUnlessDeclared(t *testing.T) {
	tests := []struct {
		name    string
		event   Event
		want    string
		wantErr string
	}{
		{name: "undeclared is absent", event: plainEvent{}},
		{name: "declared wins", event: identifiedEvent{stableID: "proposal-1"}, want: "proposal-1"},
		{name: "declared is trimmed", event: identifiedEvent{stableID: "  proposal-1 "}, want: "proposal-1"},
		{name: "empty is rejected", event: identifiedEvent{}, wantErr: "declares an empty stable identity"},
		{name: "blank is rejected", event: identifiedEvent{stableID: "   "}, wantErr: "declares an empty stable identity"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stableID, err := stableIDOf(test.event)
			if test.wantErr != "" {
				require.ErrorIs(t, err, ErrInvalidEvent)
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, stableID)
		})
	}
}

func TestTagsAreAbsentUnlessDeclared(t *testing.T) {
	require.Nil(t, tagsOf(plainEvent{}))
	require.Equal(t, []string{TagWarning}, tagsOf(taggedEvent{tags: []string{TagWarning}}))
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
