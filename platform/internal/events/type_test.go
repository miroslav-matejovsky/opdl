package events

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypeValidate(t *testing.T) {
	tests := []struct {
		name      string
		eventType Type
		wantErr   string
	}{
		{name: "domain event", eventType: "platform.registration.accepted"},
		{name: "underscored source", eventType: "platform.event_fabric.ready"},
		{name: "underscored fact", eventType: "platform.app.site_opened"},
		{name: "empty", eventType: "", wantErr: "exactly three tokens"},
		{name: "two tokens", eventType: "platform.accepted", wantErr: "exactly three tokens"},
		{name: "four tokens", eventType: "platform.registration.proposal.accepted", wantErr: "exactly three tokens"},
		{name: "missing prefix", eventType: "registration.proposal.accepted", wantErr: `must start with "platform"`},
		{name: "wrong prefix", eventType: "opdl.registration.accepted", wantErr: `must start with "platform"`},
		{name: "blank source", eventType: "platform..accepted", wantErr: "unusable source token"},
		{name: "blank fact", eventType: "platform.registration.", wantErr: "unusable fact token"},
		{name: "upper case source", eventType: "platform.Registration.accepted", wantErr: "unusable source token"},
		{name: "upper case fact", eventType: "platform.registration.Accepted", wantErr: "unusable fact token"},
		{name: "digits", eventType: "platform.registration.accepted2", wantErr: "unusable fact token"},
		{name: "hyphen", eventType: "platform.event-fabric.ready", wantErr: "unusable source token"},
		{name: "leading underscore", eventType: "platform._fabric.ready", wantErr: "unusable source token"},
		{name: "trailing underscore", eventType: "platform.fabric.ready_", wantErr: "unusable fact token"},
		{name: "doubled underscore", eventType: "platform.event__fabric.ready", wantErr: "unusable source token"},
		{name: "space", eventType: "platform.registration.was accepted", wantErr: "unusable fact token"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.eventType.Validate()
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrInvalidEventType)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestTypeSourceIsTheMiddleToken(t *testing.T) {
	tests := []struct {
		name      string
		eventType Type
		want      string
	}{
		{name: "domain event", eventType: "platform.registration.accepted", want: "registration"},
		{name: "underscored source", eventType: "platform.event_fabric.ready", want: "event_fabric"},
		{name: "malformed type has no source", eventType: "registration.accepted", want: ""},
		{name: "empty type has no source", eventType: "", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.eventType.Source())
		})
	}
}
