package health

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
)

// pollInterval is how often this scenario re-asks an instance for its state.
const pollInterval = 100 * time.Millisecond

// MonitoringSurvivesObserverLossAndFailover is the other half of the story: what
// the site says when an observer goes away rather than a service.
//
//  1. Three instances converge on both machines' services being healthy.
//  2. node-a's Standby Instance is killed. Its reports expire, every instance
//     says so by name, and node-a's service stays Healthy — its Primary Instance
//     is still watching it, and one observer falling silent is not evidence
//     about a target.
//  3. The Standby is started again. It rebuilds the whole site's picture from
//     live traffic, with no health file to have restored from, and every
//     instance goes back to a complete view.
//  4. node-a's Primary Instance is killed. Ownership moves to the Standby, and
//     node-a's service stays Healthy throughout — the surviving instance was
//     already probing it, and never stopped.
//
// The last step is why monitoring is composed at process lifetime rather than
// inside an activation. A machine whose ownership is moving is exactly when its
// services most need to still be watched.
func MonitoringSurvivesObserverLossAndFailover(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	deployment := harness.DeploySite(ctx, t,
		filepath.Join(harness.ScenarioDir(t), "out"), filepath.Join(harness.ScenarioDir(t), "work"), "health")
	nodeA, nodeB := deployment.Machine(t, "node-a"), deployment.Machine(t, "node-b")
	machines := []*harness.Machine{nodeA, nodeB}

	harness.StartService(t, nodeA)
	harness.StartService(t, nodeB)

	// Every instance is started as an explicitly managed process, so the scenario
	// can kill one of node-a's two without touching the other.
	manifest := harness.ReadManifest(t, nodeA.BinaryPath)
	require.NotNil(t, manifest.Standby, "node-a deploys a standby in the health fixture")
	primaryA := nodeA.StartManaged(ctx, t, harness.RolePrimary, manifest.Primary.Args)
	standbyA := nodeA.StartManaged(ctx, t, harness.RoleStandby, manifest.Standby.Args)
	nodeB.Start(ctx, t)
	harness.WaitForActiveInstance(ctx, t, nodeA)
	harness.WaitForActiveInstance(ctx, t, nodeB)

	observers := harness.Observers(machines...)
	diag := harness.DiagStringer(func() string {
		return fmt.Sprintf("--- node-a primary ---\n%s\n--- node-a standby ---\n%s\n%s",
			primaryA.Logs(), standbyA.Logs(), harness.Diagnose(machines))
	})

	harness.WaitForServiceViews(ctx, t, "every instance seeing the whole site healthy",
		observers, machines, func(view harness.ServiceView) bool {
			return everyServiceIsHealthy(view) && everyObserverIsFresh(view)
		})

	// -- An observer goes away. ------------------------------------------------

	require.NoError(t, standbyA.Kill())
	// The Standby's endpoint dies with it, so from here the site is watched
	// through the two instances that are still serving.
	surviving := harness.Observers(nodeB)
	surviving = append(surviving, &harness.Observer{
		Name: "node-a/primary", URL: nodeA.URL, Machine: "node-a", Role: harness.RolePrimary,
	})

	harness.WaitForServiceViews(ctx, t, "the lost observer expiring on every surviving instance",
		surviving, machines, func(view harness.ServiceView) bool {
			watched, ok := view.Unit("node-a", service)
			return ok && len(watched.StaleObservers) == 1 &&
				watched.StaleObservers[0] == harness.RoleStandby
		})

	// The service is still Healthy. A missing observer is a fact about the
	// platform, and letting it erase a live observation from the instance that is
	// still watching would report a fault where there is none.
	for _, observer := range surviving {
		view := harness.GetServiceView(ctx, t, observer, machines...)
		watched, ok := view.Unit("node-a", service)
		require.Truef(t, ok, "%s still holds node-a's service", observer.Name)
		require.Equalf(t, harness.ServiceHealthy, watched.Status,
			"%s: one observer stopping is not evidence about the target: %s", observer.Name, watched)
		require.Truef(t, watched.Fresh(harness.RolePrimary),
			"%s: the surviving observer is still reporting: %s", observer.Name, watched)
		reported, ok := watched.Observation(harness.RoleStandby)
		require.Truef(t, ok, "%s keeps what the lost observer last said: %s", observer.Name, watched)
		require.True(t, reported.Stale, "%s marks it aged rather than deleting it: %s", observer.Name, watched)
		require.Emptyf(t, watched.MissingObservers,
			"%s: an observer that stopped talking is not one that never started: %s", observer.Name, watched)
	}

	// node-b's fabric state says the same thing in one word: it is hearing from
	// some of node-a and not all of it.
	nodeBView := harness.GetServiceView(ctx, t, harness.Observers(nodeB)[0], machines...)
	require.Equal(t, "Partial", nodeBView.Distribution.State,
		"node-b hears one of node-a's two expected observers: %s", nodeBView)

	// -- and comes back with nothing to restore from. --------------------------

	standbyA = nodeA.StartManaged(ctx, t, harness.RoleStandby, manifest.Standby.Args)
	harness.WaitForServiceViews(ctx, t, "the restarted observer rejoining every instance's view",
		observers, machines, func(view harness.ServiceView) bool {
			return everyServiceIsHealthy(view) && everyObserverIsFresh(view)
		})

	// The restarted instance knows about the machine it is not on, which it can
	// only have learned from traffic that arrived after it started: nothing about
	// node-b survived its restart, because nothing about node-b was ever written
	// down.
	restarted := harness.GetServiceView(ctx, t, &harness.Observer{
		Name: "node-a/standby", URL: nodeA.StandbyURL, Machine: "node-a", Role: harness.RoleStandby,
	}, machines...)
	remote, ok := restarted.Unit("node-b", service)
	require.Truef(t, ok, "the restarted instance rebuilt the whole site: %s", restarted)
	require.Equal(t, harness.ServiceHealthy, remote.Status)
	require.True(t, remote.Fresh(harness.RolePrimary), "from live traffic, not from a file: %s", remote)

	// -- Ownership moves while the checks continue. ----------------------------

	require.NoError(t, primaryA.Kill())
	harness.WaitFor(t, "node-a's Standby Instance taking ownership", harness.ServiceViewTimeout, pollInterval,
		func() bool {
			instance, code, err := harness.FetchInstanceAt(ctx, nodeA.StandbyURL)
			return err == nil && code == http.StatusOK && instance.State == harness.InstanceStateActive
		}, nil, diag)

	// The instance that took over had been probing node-a's service the whole
	// time it was Passive, so the machine's service was never unwatched. What
	// changed is which instance answers for the machine, not what is known about
	// it.
	harness.WaitForServiceViews(ctx, t, "the machine's service still watched after ownership moved",
		harness.Observers(nodeB), machines, func(view harness.ServiceView) bool {
			watched, ok := view.Unit("node-a", service)
			return ok && watched.Status == harness.ServiceHealthy &&
				watched.Fresh(harness.RoleStandby)
		})

	final := harness.GetServiceView(ctx, t, harness.Observers(nodeB)[0], machines...)
	own, ok := final.Unit("node-b", service)
	require.True(t, ok)
	require.Equalf(t, harness.ServiceHealthy, own.Status,
		"node-b's own service was never affected by anything on node-a: %s", final)
}
