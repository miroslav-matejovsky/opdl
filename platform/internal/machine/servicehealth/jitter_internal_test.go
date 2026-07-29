package servicehealth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The jitter is tested from inside the package, like the tracker beside it,
// because what it promises is a property of a pure function rather than
// something a worker's behavior shows. Whether a worker waits it out at all is
// covered from outside, in monitor_test.go.

// TestStartupJitterIsBoundedDeterministicAndObserverSpecific pins the three
// properties the jitter exists for.
//
// Bounded, so a service's first status arrives inside one interval rather than
// whenever. Deterministic, so a restarted instance resumes the phase it had
// instead of landing on its peer's. Observer-specific, so a machine's two
// instances do not ask the same question at the same instant for as long as
// they both run.
func TestStartupJitterIsBoundedDeterministicAndObserverSpecific(t *testing.T) {
	// Enough services that two observers landing on identical phases for most of
	// them would be a hash that is not distributing rather than a coincidence.
	services := []string{
		"alarm-service", "reporting-service", "audit-service", "gateway-service",
		"index-service", "media-service", "notify-service", "search-service",
	}
	const interval = 10 * time.Second

	phasesOf := func(observer string) []time.Duration {
		phases := make([]time.Duration, 0, len(services))
		for _, service := range services {
			phase := jitterFor(observer, service, interval)
			require.GreaterOrEqualf(t, phase, time.Duration(0), "%s/%s is not negative", observer, service)
			require.Lessf(t, phase, interval, "%s/%s begins within one interval", observer, service)
			phases = append(phases, phase)
		}
		return phases
	}

	primary, standby := phasesOf("primary"), phasesOf("standby")
	require.Equal(t, primary, phasesOf("primary"),
		"the same observer and service produce the same phase, so a restart resumes rather than moves")
	require.NotEqual(t, primary, standby,
		"a machine's two instances do not probe its services in step")

	// A pair agreeing on one service would be tolerable. A pair agreeing on most
	// of them would mean the observer identity is barely reaching the hash, which
	// is the failure this whole mechanism is meant to avoid.
	agreed := 0
	for i := range services {
		if primary[i] == standby[i] {
			agreed++
		}
	}
	require.Lessf(t, agreed, len(services)/2,
		"the two observers' phases mostly coincide: %v vs %v", primary, standby)
}

// TestStartupJitterSeparatesTheObserverFromTheService checks the two halves of
// the seed cannot be shifted into each other. Without the separator byte,
// ("primary", "ab") and ("primar", "yab") would hash alike and two different
// observers would share a phase for no reason a reader could see.
func TestStartupJitterSeparatesTheObserverFromTheService(t *testing.T) {
	const interval = time.Hour
	require.NotEqual(t,
		jitterFor("primary", "ab", interval),
		jitterFor("primar", "yab", interval))
}

// TestStartupJitterIsZeroWithNoIntervalToSpreadAcross checks the guard that
// keeps the modulus from dividing by zero. A target with no interval is
// rejected before a worker is built, so this is the arithmetic being safe
// rather than a policy.
func TestStartupJitterIsZeroWithNoIntervalToSpreadAcross(t *testing.T) {
	require.Zero(t, jitterFor("primary", "alarm-service", 0))
	require.Zero(t, jitterFor("primary", "alarm-service", -time.Second))
}
