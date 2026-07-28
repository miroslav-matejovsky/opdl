package events

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// scopedEvent is an event that declares a level above the instance, which is
// what a registration or an ownership transition looks like to the stamper.
type scopedEvent struct {
	scope Scope
}

func (scopedEvent) EventType() Type { return "platform.test.plain" }

func (e scopedEvent) Scope() Scope { return e.scope }

func TestScopeValidAcceptsOnlyTheClosedSet(t *testing.T) {
	tests := []struct {
		name  string
		scope Scope
		want  bool
	}{
		{name: "site", scope: ScopeSite, want: true},
		{name: "machine", scope: ScopeMachine, want: true},
		{name: "instance", scope: ScopeInstance, want: true},
		{name: "default is instance", scope: DefaultScope, want: true},
		{name: "empty", scope: "", want: false},
		{name: "unknown level", scope: "cluster", want: false},
		{name: "wrong case", scope: "SITE", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.scope.Valid())
		})
	}
}

// TestWrapDefaultsScopeToInstance pins the safe direction: an event that
// declares no scope keeps its fact local rather than reaching the site.
func TestWrapDefaultsScopeToInstance(t *testing.T) {
	envelope, err := testFactory().Wrap(t.Context(), plainEvent{Detail: "started"})
	require.NoError(t, err)

	require.Equal(t, ScopeInstance, envelope.Scope)
}

func TestWrapStampsADeclaredScope(t *testing.T) {
	for _, scope := range []Scope{ScopeSite, ScopeMachine, ScopeInstance} {
		t.Run(string(scope), func(t *testing.T) {
			envelope, err := testFactory().Wrap(t.Context(), scopedEvent{scope: scope})
			require.NoError(t, err)
			require.Equal(t, scope, envelope.Scope)
		})
	}
}

// TestWrapRejectsAnUnknownScope checks the closed set is enforced where the
// declaration is read. Scope decides delivery, so a level nothing routes would
// be a fact that quietly never leaves the process that stated it.
func TestWrapRejectsAnUnknownScope(t *testing.T) {
	_, err := testFactory().Wrap(t.Context(), scopedEvent{scope: "cluster"})

	require.ErrorIs(t, err, ErrInvalidEvent)
	require.ErrorContains(t, err, `declares unknown scope "cluster"`)
}
