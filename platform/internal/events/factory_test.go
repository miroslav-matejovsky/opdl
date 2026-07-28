package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

// testDescriptor is the deployment identity a composed factory reads.
var testDescriptor = config.Descriptor{
	Platform:       "opdl",
	Project:        "scenario",
	Environment:    "development",
	Site:           "local",
	Machine:        "node",
	MachineProfile: "all-in-one",
	IP:             "127.0.0.1",
	Services:       []string{"core-services"},
}

// testFactory returns a factory that stamps deterministic envelopes: a fixed
// occurrence time and counted IDs, so a test asserts what the factory derives
// rather than what the clock happened to say.
func testFactory() Factory {
	stamped := 0
	return Factory{
		origin: testOrigin,
		now:    func() time.Time { return testOccurredAt },
		newID: func() (string, error) {
			stamped++
			return fmt.Sprintf("id-%d", stamped), nil
		},
	}
}

func TestNewFactoryCapturesProcessIdentity(t *testing.T) {
	factory, err := NewFactory(testDescriptor, "primary")
	require.NoError(t, err)

	require.Equal(t, Origin{
		Machine:        "node",
		MachineProfile: "all-in-one",
		ProcessRole:    "primary",
		PID:            os.Getpid(),
	}, factory.Origin())
}

func TestNewFactoryRejectsAnIncompleteIdentity(t *testing.T) {
	tests := []struct {
		name        string
		descriptor  func(config.Descriptor) config.Descriptor
		processRole string
		wantErr     string
	}{
		{
			name:       "missing machine",
			descriptor: func(d config.Descriptor) config.Descriptor { d.Machine = ""; return d },
			wantErr:    "origin machine is required",
		},
		{
			name:       "missing process role",
			descriptor: func(d config.Descriptor) config.Descriptor { return d },
			wantErr:    "origin process_role is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewFactory(test.descriptor(testDescriptor), test.processRole)
			require.ErrorContains(t, err, "compose factory")
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestWrapDerivesEverythingAPlainEventDoesNotDeclare(t *testing.T) {
	envelope, err := testFactory().Wrap(t.Context(), plainEvent{Detail: "started"})
	require.NoError(t, err)
	require.NoError(t, envelope.Validate())

	require.Equal(t, Envelope{
		ID:            "id-1",
		Type:          "platform.test.plain",
		SchemaVersion: DefaultSchemaVersion,
		OccurredAt:    testOccurredAt,
		Source:        "test",
		Severity:      DefaultSeverity,
		Scope:         DefaultScope,
		Origin:        testOrigin,
		Data:          json.RawMessage(`{"detail":"started"}`),
	}, envelope)
}

func TestWrapStampsUTCOccurrenceTime(t *testing.T) {
	factory := testFactory()
	factory.now = func() time.Time { return testOccurredAt.In(time.FixedZone("CET", 3600)) }

	envelope, err := factory.Wrap(t.Context(), plainEvent{})
	require.NoError(t, err)
	require.Equal(t, time.UTC, envelope.OccurredAt.Location())
	require.True(t, testOccurredAt.Equal(envelope.OccurredAt))
}

func TestWrapTakesTheMetadataAnEventDeclares(t *testing.T) {
	factory := testFactory()

	versioned, err := factory.Wrap(t.Context(), versionedEvent{version: 3})
	require.NoError(t, err)
	require.Equal(t, 3, versioned.SchemaVersion)

	severe, err := factory.Wrap(t.Context(), severeEvent{severity: SeverityWarn})
	require.NoError(t, err)
	require.Equal(t, SeverityWarn, severe.Severity)

	tagged, err := factory.Wrap(t.Context(), taggedEvent{tags: []string{" warning ", "audit", "warning"}})
	require.NoError(t, err)
	require.Equal(t, []string{"audit", "warning"}, tagged.Tags, "declared tags are normalized once, here")

	identified, err := factory.Wrap(t.Context(), identifiedEvent{stableID: "proposal-1"})
	require.NoError(t, err)
	require.Equal(t, "proposal-1", identified.StableID)
}

func TestWrapGivesEachOccurrenceItsOwnIdentityAndTime(t *testing.T) {
	factory := testFactory()
	tick := testOccurredAt
	factory.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	first, err := factory.Wrap(t.Context(), plainEvent{})
	require.NoError(t, err)
	second, err := factory.Wrap(t.Context(), plainEvent{})
	require.NoError(t, err)

	require.NotEqual(t, first.ID, second.ID)
	require.True(t, first.OccurredAt.Before(second.OccurredAt))
	require.Equal(t, first.Origin, second.Origin, "one runtime states one origin")
}

func TestWrapTakesCausalLinksFromAHandlerContext(t *testing.T) {
	factory := testFactory()

	uncaused, err := factory.Wrap(t.Context(), plainEvent{})
	require.NoError(t, err)
	require.Empty(t, uncaused.CausationID)
	require.Empty(t, uncaused.CorrelationID)

	started, err := factory.Wrap(WithCause(t.Context(), Envelope{ID: "proposal"}), plainEvent{})
	require.NoError(t, err)
	require.Equal(t, "proposal", started.CausationID)
	require.Equal(t, "proposal", started.CorrelationID)

	continued, err := factory.Wrap(WithCause(t.Context(), started), plainEvent{})
	require.NoError(t, err)
	require.Equal(t, started.ID, continued.CausationID)
	require.Equal(t, "proposal", continued.CorrelationID, "the workflow keeps the identity it started with")
}

func TestWrapRefusesAnEventItCannotStamp(t *testing.T) {
	tests := []struct {
		name    string
		event   Event
		wantErr string
	}{
		{name: "malformed type", event: malformedTypeEvent{}, wantErr: "invalid event type"},
		{name: "unusable schema version", event: versionedEvent{version: 0}, wantErr: "declares schema version 0"},
		{name: "unknown severity", event: severeEvent{severity: "fatal"}, wantErr: "declares unknown severity"},
		{name: "empty stable identity", event: identifiedEvent{}, wantErr: "declares an empty stable identity"},
		{name: "unencodable payload", event: brokenEvent{}, wantErr: "encode platform.test.broken payload"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope, err := testFactory().Wrap(t.Context(), test.event)
			require.ErrorContains(t, err, test.wantErr)
			require.Zero(t, envelope, "a refused event stamps nothing")
		})
	}
}

func TestWrapRefusesAnUncomposedFactory(t *testing.T) {
	_, err := Factory{}.Wrap(t.Context(), plainEvent{})
	require.ErrorIs(t, err, ErrUncomposedFactory)
}

func TestWrapReportsOccurrenceIDFailure(t *testing.T) {
	factory := testFactory()
	factory.newID = func() (string, error) { return "", errors.New("random source failed") }

	envelope, err := factory.Wrap(t.Context(), plainEvent{})
	require.ErrorContains(t, err, "random source failed")
	require.Zero(t, envelope)
}

// malformedTypeEvent declares a type that is not platform.<source>.<fact>.
type malformedTypeEvent struct{}

func (malformedTypeEvent) EventType() Type { return "test.malformed" }
