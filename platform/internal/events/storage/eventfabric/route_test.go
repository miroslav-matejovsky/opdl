package eventfabric

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
)

func TestParseEventTypeSplitsDomainAndFact(t *testing.T) {
	parsed, err := ParseEventType("platform.registration.accepted")
	require.NoError(t, err)
	require.Equal(t, EventType{Domain: "registration", Fact: "accepted"}, parsed)
}

func TestParseEventTypeRejectsMalformedTypes(t *testing.T) {
	tests := []struct {
		name      string
		eventType events.Type
	}{
		{name: "empty", eventType: ""},
		{name: "one token", eventType: "platform"},
		{name: "two tokens", eventType: "platform.registration"},
		{name: "four tokens", eventType: "platform.registration.key.conflict"},
		{name: "wrong prefix", eventType: "opdl.registration.accepted"},
		{name: "blank domain", eventType: "platform..accepted"},
		{name: "blank fact", eventType: "platform.registration."},
		{name: "trailing dot adds a blank token", eventType: "platform.registration.accepted."},
		{name: "leading dot", eventType: ".registration.accepted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseEventType(test.eventType)
			require.ErrorIs(t, err, ErrInvalidEventType)
		})
	}
}

func TestNewSiteScopeIsStableAndSafe(t *testing.T) {
	scope := NewSiteScope("customer-a", "production", "north")

	require.Equal(t, scope, NewSiteScope("customer-a", "production", "north"))

	require.NotEmpty(t, scope)
	for _, r := range string(scope) {
		isLowerBase32 := (r >= 'a' && r <= 'z') || (r >= '2' && r <= '7')
		require.True(t, isLowerBase32, "scope token %q is not a safe subject token", scope)
	}
}

func TestSafeTokenIsStableAndDistinct(t *testing.T) {
	require.Equal(t, SafeToken("node-a"), SafeToken("node-a"), "stable for the same value")
	require.NotEqual(t, SafeToken("node-a"), SafeToken("node-b"), "distinct for different values")
	for _, r := range SafeToken("odd/machine name") {
		safe := (r >= 'a' && r <= 'z') || (r >= '2' && r <= '7')
		require.True(t, safe, "token must be a safe subject token")
	}
}

func TestNewSiteScopeIsLengthPrefixedAgainstBoundaryCollisions(t *testing.T) {
	require.NotEqual(t,
		NewSiteScope("ab", "c", "d"),
		NewSiteScope("a", "bc", "d"),
	)
	require.NotEqual(t,
		NewSiteScope("a", "b", "cd"),
		NewSiteScope("a", "bc", "d"),
	)
}

func TestNewSiteScopeIsolatesSites(t *testing.T) {
	base := NewSiteScope("project", "environment", "site")
	require.NotEqual(t, base, NewSiteScope("other", "environment", "site"))
	require.NotEqual(t, base, NewSiteScope("project", "other", "site"))
	require.NotEqual(t, base, NewSiteScope("project", "environment", "other"))
}

func TestSiteScopeStreamAndFilterAreDerivedFromTheScope(t *testing.T) {
	scope := NewSiteScope("customer-a", "production", "north")

	require.Equal(t, "OPDL_"+upper(string(scope))+"_EVENTS", scope.StreamName())
	require.Equal(t, "opdl."+string(scope)+".event.>", scope.SubjectFilter())
}

func TestRouteSubjectPlacesTheEventUnderItsSiteScope(t *testing.T) {
	scope := NewSiteScope("customer-a", "production", "north")

	route, err := NewRoute(scope, "platform.registration.proposed")
	require.NoError(t, err)
	require.Equal(t, "opdl."+string(scope)+".event.registration.proposed", route.Subject())
}

func TestNewRouteRejectsMalformedEventTypes(t *testing.T) {
	_, err := NewRoute(NewSiteScope("p", "e", "s"), "registration.proposed")
	require.ErrorIs(t, err, ErrInvalidEventType)
}

func TestRoutesOfOneSiteAreCapturedByItsJournalFilter(t *testing.T) {
	scope := NewSiteScope("customer-a", "production", "north")
	route, err := NewRoute(scope, "platform.registration.accepted")
	require.NoError(t, err)

	prefix := "opdl." + string(scope) + ".event."
	require.Equal(t, prefix, route.Subject()[:len(prefix)])
	require.Equal(t, prefix+">", scope.SubjectFilter())
}

func upper(scope string) string {
	out := []byte(scope)
	for i, b := range out {
		if b >= 'a' && b <= 'z' {
			out[i] = b - ('a' - 'A')
		}
	}
	return string(out)
}
