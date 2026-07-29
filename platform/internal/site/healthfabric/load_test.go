package healthfabric_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/natsserver"
	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthview"
	"github.com/miroslav-matejovsky/opdl/utils/testnet"
)

// This file measures the health fabric at the site size the platform commits to
// supporting. The numbers below are that commitment, not a convenient size to
// test at; changing one of them changes what the platform claims.
//
// The whole site runs in one process against one embedded broker. That is not a
// deployment — a real site has sixteen processes on eight machines — but it is
// the harder case for the parts being measured: every subscription, every view,
// and every publisher's buffer is contending for one machine's memory and one
// broker's delivery goroutines at once.
const (
	// loadMachines is the maximum machines one site deploys.
	loadMachines = 8
	// loadServicesPerMachine is the maximum services one machine hosts.
	loadServicesPerMachine = 16
	// loadInterval is the shortest probe interval a service may be authored with.
	loadInterval = time.Second
	// loadTimeout is the longest timeout one attempt may be given. It is shorter
	// than the interval by the descriptor's own rule.
	loadTimeout = 900 * time.Millisecond
	// loadRounds is how many intervals the measurement runs for.
	loadRounds = 3
)

// The derived envelope. Every instance observes every service on its own
// machine, and every observation is fanned to every instance at the site.
const (
	// loadUnits is the size of the site inventory each instance holds.
	loadUnits = loadMachines * loadServicesPerMachine
	// loadInstances is how many platform processes the site runs: two per
	// machine, because a Standby observes for its whole lifetime.
	loadInstances = loadMachines * 2
	// loadObservationsPerRound is how many observations the whole site produces
	// in one interval.
	loadObservationsPerRound = loadInstances * loadServicesPerMachine
)

// loadFreshFor is the freshness the descriptor derives for these timings. A
// report stays current for one missed interval, which is what keeps a site
// publishing once per interval from expiring itself.
const loadFreshFor = 2*loadInterval + loadTimeout

// TestSiteFanOutSustainsTheSupportedEnvelope runs the whole supported site
// through one broker and checks that what it produces stays bounded and arrives.
//
// Three things are being measured, and each of them is a claim made elsewhere:
//
//   - the publisher's buffer is bounded by a machine's service count rather than
//     by how long the site has been running, so nothing accumulates;
//   - every observation is accounted for — sent, superseded by a newer one about
//     the same service, or refused — with none left unexplained; and
//   - every instance converges on the same complete view, with both observers
//     current on all 128 services.
func TestSiteFanOutSustainsTheSupportedEnvelope(t *testing.T) {
	if testing.Short() {
		t.Skip("starts an embedded NATS server and runs the site for several seconds")
	}

	broker := startLoadBroker(t)
	inventory := loadInventory()
	require.Len(t, inventory, loadUnits)

	site := make([]*loadInstance, 0, loadInstances)
	for machine := range loadMachines {
		for _, role := range []string{"primary", "standby"} {
			site = append(site, startLoadInstance(t, broker, inventory, loadMachineName(machine), role))
		}
	}
	require.Len(t, site, loadInstances)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	startedAt := time.Now()

	// Every instance publishes its own machine's services once per interval, the
	// cadence a worker at the minimum interval produces.
	published := 0
	for round := range loadRounds {
		for _, instance := range site {
			for service := range loadServicesPerMachine {
				// Stamped by the publisher and applied here, exactly as composition
				// does it: an instance never receives its own messages back from the
				// broker, so its own machine reaches its own view directly.
				stamped := instance.publisher.Publish(healthview.Observation{
					Unit:         healthview.UnitKey{Service: loadServiceName(service)},
					Status:       healthview.StatusHealthy,
					CheckedAtUTC: time.Now().UTC(),
					Latency:      4 * time.Millisecond,
				})
				require.Equal(t, healthview.DropNone, instance.view.Apply(stamped))
				published++
			}
		}
		if round < loadRounds-1 {
			time.Sleep(loadInterval)
		}
	}
	require.Equal(t, loadObservationsPerRound*loadRounds, published)

	// Every instance ends up knowing about every service at the site, from both
	// of the observers expected to report on it. Convergence is the assertion;
	// the counters below say what it cost.
	require.Eventually(t, func() bool {
		for _, instance := range site {
			if !loadViewIsComplete(instance.view.Snapshot()) {
				return false
			}
		}
		return true
	}, settle, 20*time.Millisecond, "every instance converged on the whole site")
	elapsed := time.Since(startedAt)

	runtime.GC()
	runtime.ReadMemStats(&after)

	// Nothing a publisher accepted went missing. A pending observation is either
	// sent or replaced by a newer one about the same service; the buffer holds one
	// per service, so an outage costs supersessions rather than growth.
	totalSent, totalSuperseded, totalFailed := uint64(0), uint64(0), uint64(0)
	for _, instance := range site {
		counters := instance.publisher.Counters()
		require.Zerof(t, counters.Failed, "%s had publications refused", instance.name)
		accounted := counters.Published + counters.Superseded + counters.Failed
		require.Equalf(t, uint64(loadServicesPerMachine*loadRounds), accounted,
			"%s: every observation it accepted is accounted for", instance.name)
		totalSent += counters.Published
		totalSuperseded += counters.Superseded
		totalFailed += counters.Failed
	}

	// Nothing that arrived was malformed or about another deployment, and nothing
	// well-formed was refused by a view. Either would mean the site converged
	// despite the fabric rather than through it.
	totalDelivered := uint64(0)
	for _, instance := range site {
		counters := instance.subscriber.Counters()
		totalDelivered += counters.Delivered
		for reason, count := range counters.Rejected {
			require.Zerof(t, count, "%s rejected %d messages as %s", instance.name, count, reason)
		}
		for reason, count := range counters.Dropped {
			require.Zerof(t, count, "%s dropped %d observations as %s", instance.name, count, reason)
		}
	}

	// A snapshot at the full inventory is what the API renders on every request,
	// so its cost is a request's cost. Rendered repeatedly because one render is
	// below the resolution of the clock this host measures with.
	const renders = 200
	renderStart := time.Now()
	var snapshot healthview.Snapshot
	for range renders {
		snapshot = site[0].view.Snapshot()
	}
	renderCost := time.Since(renderStart) / renders
	require.Len(t, snapshot.Units, loadUnits)
	require.Equal(t, loadUnits, snapshot.Summary.Healthy)

	t.Logf("supported envelope: %d machines x %d services, %s interval, %d instances",
		loadMachines, loadServicesPerMachine, loadInterval, loadInstances)
	t.Logf("steady state: %d observations per interval, %d per second",
		loadObservationsPerRound, loadObservationsPerRound)
	t.Logf("measured over %s: %d published, %d superseded, %d failed, %d delivered site-wide",
		elapsed.Round(time.Millisecond), totalSent, totalSuperseded, totalFailed, totalDelivered)
	t.Logf("heap after convergence: %.1f MiB, up %.1f MiB from before the run",
		float64(after.HeapAlloc)/(1<<20), float64(after.HeapAlloc-before.HeapAlloc)/(1<<20))
	t.Logf("snapshot of %d units rendered in %s", loadUnits, renderCost.Round(time.Microsecond))
}

// TestOneObservationStaysInsideItsBoundAtTheSupportedSize checks the wire cost
// of the envelope from the other end: one message, at the largest identity the
// site produces.
func TestOneObservationStaysInsideItsBoundAtTheSupportedSize(t *testing.T) {
	conn := newFakeConn()
	publisher, err := healthfabric.NewPublisher(conn, healthfabric.Identity{
		Deployment:   deployment,
		Machine:      loadMachineName(loadMachines - 1),
		ObserverRole: "standby",
		Epoch:        1 << 40,
	}, discardLog())
	require.NoError(t, err)
	t.Cleanup(publisher.Close)

	publisher.Publish(healthview.Observation{
		Unit:         healthview.UnitKey{Service: loadServiceName(loadServicesPerMachine - 1)},
		Status:       healthview.StatusUnhealthy,
		CheckedAtUTC: time.Now().UTC(),
		Latency:      loadTimeout,
		// The longest failure text a probe produces, near the bound the decoder
		// enforces on it. Printable, because a real one is: an error full of bytes
		// that JSON escapes six characters wide would measure the escaping rather
		// than the message.
		Error:               strings.Repeat("connection refused by 10.0.1.10:9101; ", 11),
		ConsecutiveFailures: 9,
	})
	conn.awaitSent(t)

	message := conn.messages()[0]
	require.Less(t, len(message), healthfabric.MaxMessageBytes,
		"one observation at the supported size stays inside the bound receivers enforce")
	t.Logf("one observation at the supported size is %d bytes, bound %d",
		len(message), healthfabric.MaxMessageBytes)
	t.Logf("steady-state wire cost: %d observations per interval x %d bytes = %d bytes/s site-wide",
		loadObservationsPerRound, len(message), loadObservationsPerRound*len(message))
}

// loadInstance is one platform process's half of the health fabric.
type loadInstance struct {
	name       string
	view       *healthview.View
	publisher  *healthfabric.Publisher
	subscriber *healthfabric.Subscriber
}

// startLoadBroker brings up the one embedded broker the whole simulated site
// connects to.
func startLoadBroker(t *testing.T) *natsserver.Server {
	t.Helper()
	reservation, err := testnet.Reserve(t.Context(), 1)
	require.NoError(t, err)
	address := reservation.Addresses()[0]
	require.NoError(t, reservation.Release())

	broker, err := natsserver.Start(natsserver.Config{
		Name:           "health-load",
		ClusterName:    "health-load-site",
		ClusterAddress: address,
	})
	require.NoError(t, err)
	t.Cleanup(broker.Close)
	return broker
}

// startLoadInstance composes one instance's connection, view, subscription, and
// publisher, in the order composition does.
func startLoadInstance(t *testing.T, broker healthfabric.InProcessConnProvider, inventory []healthview.Unit, machine, role string) *loadInstance {
	t.Helper()
	name := machine + "/" + role

	conn, err := healthfabric.Connect(broker, "health-"+name)
	require.NoError(t, err)
	t.Cleanup(conn.Close)

	view, err := healthview.New(deployment, inventory, healthview.SystemClock{})
	require.NoError(t, err)

	subscriber, err := healthfabric.Subscribe(t.Context(), conn, view, discardLog())
	require.NoError(t, err)
	t.Cleanup(subscriber.Close)

	publisher, err := healthfabric.NewPublisher(conn, healthfabric.Identity{
		Deployment: deployment, Machine: machine, ObserverRole: role, Epoch: 1,
	}, discardLog())
	require.NoError(t, err)
	t.Cleanup(publisher.Close)

	return &loadInstance{name: name, view: view, publisher: publisher, subscriber: subscriber}
}

// loadInventory is the static site inventory every instance is built from at
// the supported size.
func loadInventory() []healthview.Unit {
	units := make([]healthview.Unit, 0, loadUnits)
	for machine := range loadMachines {
		for service := range loadServicesPerMachine {
			units = append(units, healthview.Unit{
				UnitKey: healthview.UnitKey{
					Machine: loadMachineName(machine),
					Service: loadServiceName(service),
				},
				MachineProfile: "all-in-one",
				ServiceRole:    "master",
				ObserverRoles:  []string{"primary", "standby"},
				FreshFor:       loadFreshFor,
			})
		}
	}
	return units
}

// loadViewIsComplete reports whether a snapshot holds a current report from
// every expected observer on every service.
func loadViewIsComplete(snapshot healthview.Snapshot) bool {
	if len(snapshot.Units) != loadUnits {
		return false
	}
	for _, unit := range snapshot.Units {
		if unit.Status != healthview.StatusHealthy ||
			len(unit.MissingObservers) > 0 || len(unit.StaleObservers) > 0 {
			return false
		}
	}
	return true
}

func loadMachineName(index int) string { return fmt.Sprintf("node-%02d", index) }

func loadServiceName(index int) string { return fmt.Sprintf("service-%02d", index) }
