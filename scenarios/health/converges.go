package health

import (
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/harness"
)

// service is the name every scenario machine's blueprint authors. One per
// machine, so a site's inventory is one entry per machine.
const service = "core-services"

// SiteConvergesAndTracksTargets is the distributed health story, end to end,
// against real processes:
//
//  1. Two machines on different addresses come up, three platform instances
//     between them, each with a service the scenario controls. Every instance
//     converges on the same picture of both machines' services.
//  2. One machine's service starts answering 503. Every instance — including the
//     two on the other machine, which never probe it — reports it Unhealthy.
//  3. Neither the failing service nor the machine hosting it changes any
//     instance's own health or readiness.
//  4. The service recovers. Every instance goes back to Healthy.
//
// Nothing here is persisted. The site's picture of itself is rebuilt from live
// traffic on every probe interval, and the scenario proves the absence of a
// health file by naming every file the deployment is entitled to write.
func SiteConvergesAndTracksTargets(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	workDir := filepath.Join(harness.ScenarioDir(t), "work")
	deployment := harness.DeploySite(ctx, t, filepath.Join(harness.ScenarioDir(t), "out"), workDir, "health")
	nodeA, nodeB := deployment.Machine(t, "node-a"), deployment.Machine(t, "node-b")
	machines := []*harness.Machine{nodeA, nodeB}

	// The services come up before the platform does, so the first probe finds
	// them rather than a connection refusal. What happens when it does not is the
	// subject of the second half.
	serviceA, serviceB := harness.StartService(t, nodeA), harness.StartService(t, nodeB)
	deployment.StartSite(ctx, t)

	// Three instances: node-a deploys a Standby and node-b does not, so node-a's
	// service has two observers and node-b's has one. Both cases are in one site
	// on purpose — a site of identical machines exercises neither.
	observers := harness.Observers(machines...)
	require.Len(t, observers, 3)

	harness.WaitForServiceViews(ctx, t, "every instance seeing the whole site healthy",
		observers, machines, func(view harness.ServiceView) bool {
			return everyServiceIsHealthy(view) && everyObserverIsFresh(view) &&
				view.Distribution.State == harness.DistributionConnected
		})

	// Each instance answers for itself, and each of them holds the whole site.
	// The endpoint exists so an operator can ask either instance of a machine and
	// get an answer, which is only worth anything if it says whose answer it is.
	views := make([]harness.ServiceView, 0, len(observers))
	for _, observer := range observers {
		view := harness.GetServiceView(ctx, t, observer, machines...)
		require.Equal(t, observer.Machine, view.Machine, "%s says which machine's view this is", observer.Name)
		require.Equal(t, observer.Role, view.Role, "%s says which of the machine's instances answered", observer.Name)
		require.Equal(t, "local", view.Site)
		require.Len(t, view.Services, 2, "%s holds both machines' services, not only its own", observer.Name)
		views = append(views, view)
	}
	same, statuses := harness.SameServiceStatuses(views)
	require.Truef(t, same, "the instances do not agree about the site: %v", statuses)

	// The remote half of each view came off the wire. An instance's own reports
	// reach its view directly, so what proves the fabric is carrying anything is
	// the entry for the machine this instance is not on.
	nodeAView := views[0]
	remote, found := nodeAView.Unit("node-b", service)
	require.True(t, found, "node-a holds an entry for node-b's service")
	require.Equal(t, []string{harness.RolePrimary}, remote.ExpectedObservers,
		"node-b deploys no standby, so one observer is expected on its service")
	require.True(t, remote.Fresh(harness.RolePrimary), "and it is reporting: %s", remote)

	local, found := nodeAView.Unit("node-a", service)
	require.True(t, found)
	require.Equal(t, []string{harness.RolePrimary, harness.RoleStandby}, local.ExpectedObservers,
		"node-a deploys both instances, so both are expected to report on its service")
	require.Len(t, local.Observations, 2, "and both have: %s", local)

	// Both services were genuinely probed, at the address their machine's
	// blueprint authored. A site that converged on Healthy without this would
	// have converged on an assumption.
	require.Positivef(t, serviceA.Requests(), "node-a's service was probed: %s", serviceA)
	require.Positivef(t, serviceB.Requests(), "node-b's service was probed: %s", serviceB)

	// -- One service starts failing. -------------------------------------------

	serviceB.Unwell()
	harness.WaitForServiceViews(ctx, t, "every instance seeing node-b's service fail",
		observers, machines, func(view harness.ServiceView) bool {
			failing, ok := view.Unit("node-b", service)
			healthy, alsoOk := view.Unit("node-a", service)
			return ok && alsoOk &&
				failing.Status == harness.ServiceUnhealthy && healthy.Status == harness.ServiceHealthy
		})

	// The observers that never probed it say so too, and say why. That is the
	// whole point of distributing observations rather than each instance knowing
	// only its own machine.
	for _, observer := range observers {
		view := harness.GetServiceView(ctx, t, observer, machines...)
		failing, _ := view.Unit("node-b", service)
		reported, ok := failing.Observation(harness.RolePrimary)
		require.Truef(t, ok, "%s has node-b's primary's report: %s", observer.Name, failing)
		require.Equal(t, harness.ServiceUnhealthy, reported.Status)
		require.NotEmptyf(t, reported.Error, "%s carries why the probe failed: %s", observer.Name, failing)
		require.GreaterOrEqualf(t, reported.ConsecutiveFailures, 1,
			"%s carries how far down it was: %s", observer.Name, failing)
	}

	// -- and none of that is the platform's problem. ---------------------------
	//
	// Both of a machine's instances probe the same targets, so a failing service
	// is not something ownership can move away from. If it reached platform
	// health, a Standby would promote through a fault promotion cannot fix.
	for _, observer := range observers {
		health, code, err := harness.FetchHealthAt(ctx, observer.URL)
		require.NoErrorf(t, err, "%s answers for its own health", observer.Name)
		require.Equal(t, http.StatusOK, code)
		require.NotEqualf(t, "Unhealthy", health.Status,
			"%s reports itself %s over a target it does not control", observer.Name, health.Status)
	}
	instance, code := harness.GetInstance(ctx, t, nodeA)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, harness.InstanceStateActive, instance.State,
		"node-a's Primary Instance still holds ownership; nothing about a target moved it")

	// -- and it recovers. ------------------------------------------------------
	//
	// One success recovers a target: the failure count exists to keep a flapping
	// endpoint from being called Unhealthy, not to make a working one wait.

	serviceB.Well()
	harness.WaitForServiceViews(ctx, t, "every instance seeing node-b's service recover",
		observers, machines, func(view harness.ServiceView) bool {
			return everyServiceIsHealthy(view) && everyObserverIsFresh(view)
		})

	requireNoHealthState(t, workDir, machines)
}

// everyServiceIsHealthy reports whether every service in a view is Healthy. An
// empty view is not: a site with no services in it is a descriptor that lost its
// inventory, not a site where all is well.
func everyServiceIsHealthy(view harness.ServiceView) bool {
	if len(view.Services) == 0 {
		return false
	}
	for _, unit := range view.Services {
		if unit.Status != harness.ServiceHealthy {
			return false
		}
	}
	return true
}

// everyObserverIsFresh reports whether every service in a view has a current
// report from every observer expected to make one.
func everyObserverIsFresh(view harness.ServiceView) bool {
	for _, unit := range view.Services {
		if len(unit.MissingObservers) > 0 || len(unit.StaleObservers) > 0 {
			return false
		}
	}
	return true
}

// requireNoHealthState proves the deployment wrote no health results anywhere.
//
// It names every file each machine is entitled to write rather than looking for
// files whose names suggest health, because a persistence file nobody predicted
// would be called something nobody predicted. Every one of these is authored in
// the blueprint; anything else under the work directory is a file the runtime
// invented, which for this feature is the failure.
func requireNoHealthState(t *testing.T, workDir string, machines []*harness.Machine) {
	t.Helper()

	permitted := make(map[string]bool)
	for _, m := range machines {
		for _, authored := range []string{
			m.Sockets.MachineEventsFile, m.Sockets.EventsFile, m.Sockets.StateFile, m.Sockets.LogFile,
			m.Sockets.StandbyEventsFile, m.Sockets.StandbyStateFile, m.Sockets.StandbyLogFile,
		} {
			if authored != "" {
				permitted[strings.ToLower(filepath.Clean(authored))] = true
			}
		}
		// The lease is the machine's, not an instance's, and the harness does not
		// carry its path in Sockets. It sits beside the machine's event store.
		permitted[strings.ToLower(filepath.Join(filepath.Dir(filepath.Clean(m.Sockets.MachineEventsFile)), "lease"))] = true
	}

	var unexpected []string
	seen := make(map[string]bool)
	require.NoError(t, filepath.WalkDir(workDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		cleaned := strings.ToLower(filepath.Clean(path))
		seen[cleaned] = true
		if !permitted[cleaned] {
			unexpected = append(unexpected, path)
		}
		return nil
	}))
	slices.Sort(unexpected)
	require.Emptyf(t, unexpected,
		"the deployment wrote files no blueprint authored; health results are ephemeral and must reach no disk:\n%s",
		strings.Join(unexpected, "\n"))

	// The walk found the files it was supposed to. Without this the check above
	// passes over a directory it failed to read, which would be a scenario that
	// proves the absence of a file by proving the absence of every file.
	for _, m := range machines {
		for _, written := range []string{m.Sockets.MachineEventsFile, m.Sockets.EventsFile, m.Sockets.LogFile} {
			require.Truef(t, seen[strings.ToLower(filepath.Clean(written))],
				"%s did not write %s, so the walk that found no health file found nothing at all",
				m.Name, written)
		}
	}
}
