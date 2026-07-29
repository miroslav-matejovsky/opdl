package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

// siteView is the view the tests below serve. It is one machine — this
// instance's own — carrying one service, which is the smallest arrangement in
// which the monitor check has anything to decide.
func siteView(status string, missing, stale []string) api.ServiceHealthResponse {
	return api.ServiceHealthResponse{
		Project:        "customer-a",
		Environment:    "production",
		Site:           "north",
		Machine:        "node-a",
		Role:           api.InstanceRolePrimary,
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		Summary:        api.ServiceHealthSummary{Unhealthy: 1},
		Services: []api.ServiceHealthUnit{{
			Machine:           "node-a",
			MachineProfile:    "local-server",
			Service:           "alarm-service",
			ServiceRole:       "master",
			Status:            status,
			ExpectedObservers: []string{api.InstanceRolePrimary},
			MissingObservers:  missing,
			StaleObservers:    stale,
			Observations: []api.ServiceHealthObservation{{
				ObserverRole:        api.InstanceRolePrimary,
				Status:              status,
				CheckedAtUTC:        time.Now().UTC().Format(time.RFC3339),
				ReceivedAtUTC:       time.Now().UTC().Format(time.RFC3339),
				ConsecutiveFailures: 3,
				Error:               "connection refused",
			}},
		}},
		Distribution: api.ServiceHealthDistribution{State: api.DistributionLocal},
	}
}

// serveView serves the health surface of an instance whose service view is the
// given one.
func serveView(t *testing.T, view api.ServiceHealthResponse) *httptest.Server {
	t.Helper()
	return serve(t, api.NewHealth(api.Deps{
		Instance:      testIdentity,
		Started:       time.Now(),
		ServiceHealth: func() api.ServiceHealthResponse { return view },
	}))
}

// TestAFailingSiteIsStillAnsweredWithOK proves the endpoint reports data rather
// than status. A site of failing services is a successful answer to the
// question asked; an error would say the platform could not answer it.
func TestAFailingSiteIsStillAnsweredWithOK(t *testing.T) {
	srv := serveView(t, siteView(api.ServiceStatusUnhealthy, nil, nil))

	response := get(t, srv.URL+api.PathHealthServices)
	require.Equal(t, http.StatusOK, response.StatusCode)

	var view api.ServiceHealthResponse
	decode(t, response, &view)
	require.Len(t, view.Services, 1)
	require.Equal(t, api.ServiceStatusUnhealthy, view.Services[0].Status)
	require.Equal(t, "connection refused", view.Services[0].Observations[0].Error)
}

// TestFailingTargetsDoNotReachPlatformHealthOrReadiness is the coupling the
// decisions forbid. Both of a machine's instances probe the same targets, so a
// failing service is not something ownership can move away from; if it reached
// this endpoint, a Standby would promote through a fault promotion cannot fix.
func TestFailingTargetsDoNotReachPlatformHealthOrReadiness(t *testing.T) {
	srv := serveView(t, siteView(api.ServiceStatusUnhealthy, nil, nil))

	var health api.HealthResponse
	decode(t, get(t, srv.URL+api.PathHealth), &health)
	require.Equal(t, api.HealthStatusHealthy, health.Status)
	require.Equal(t, api.HealthStatusHealthy, health.Checks[api.HealthCheckServiceMonitor])

	var ready api.HealthReadyResponse
	decode(t, get(t, srv.URL+api.PathHealthReady), &ready)
	require.Equal(t, api.HealthStatusHealthy, ready.Status)
}

// TestAStalledMonitorDegradesTheInstanceRatherThanFailingIt covers the fault the
// check does exist for: this instance's own reports about its own machine have
// expired, so it has stopped watching rather than found something. It is a
// platform fault and it is reported, but Unhealthy is the gate a peer promotes
// through and the peer's monitor is no better placed than this one.
func TestAStalledMonitorDegradesTheInstanceRatherThanFailingIt(t *testing.T) {
	srv := serveView(t, siteView(api.ServiceStatusUnknown, nil, []string{api.InstanceRolePrimary}))

	var health api.HealthResponse
	decode(t, get(t, srv.URL+api.PathHealth), &health)
	require.Equal(t, api.HealthStatusDegraded, health.Status)
	require.Equal(t, api.HealthStatusUnhealthy, health.Checks[api.HealthCheckServiceMonitor])
	require.NotEqual(t, api.HealthStatusUnhealthy, health.Status)
}

// TestAnObserverThatHasNotReportedYetIsNotAStalledMonitor separates the two
// absences. A process whose monitor could not start never serves anything, so
// the only thing a missing local observer can mean here is that the first
// interval has not elapsed — and reporting that as a fault would make every
// instance Degraded for its first moments.
func TestAnObserverThatHasNotReportedYetIsNotAStalledMonitor(t *testing.T) {
	srv := serveView(t, siteView(api.ServiceStatusUnknown, []string{api.InstanceRolePrimary}, nil))

	var health api.HealthResponse
	decode(t, get(t, srv.URL+api.PathHealth), &health)
	require.Equal(t, api.HealthStatusHealthy, health.Status)
	require.Equal(t, api.HealthStatusHealthy, health.Checks[api.HealthCheckServiceMonitor])
}

// TestAStaleObserverOnAnotherMachineIsNotThisMonitorsFault keeps the check
// about this instance. A silent observer elsewhere is visible in the service
// view, where it belongs; charging it to this machine's monitor would make one
// machine's outage degrade every instance at the site.
func TestAStaleObserverOnAnotherMachineIsNotThisMonitorsFault(t *testing.T) {
	view := siteView(api.ServiceStatusUnknown, nil, []string{api.InstanceRolePrimary})
	view.Services[0].Machine = "node-b"

	var health api.HealthResponse
	decode(t, get(t, serveView(t, view).URL+api.PathHealth), &health)
	require.Equal(t, api.HealthStatusHealthy, health.Status)
	require.Equal(t, api.HealthStatusHealthy, health.Checks[api.HealthCheckServiceMonitor])
}

// TestAnInstanceWithNoServiceMonitoringAnswersAnEmptyView is what spec
// generation and the boundary tests serve. An empty view is the honest answer:
// this build knows of no service. It reports no monitor check for the same
// reason it reports no fabric check — there is nothing running to report on.
func TestAnInstanceWithNoServiceMonitoringAnswersAnEmptyView(t *testing.T) {
	srv := serve(t, api.Handlers{})

	response := get(t, srv.URL+api.PathHealthServices)
	require.Equal(t, http.StatusOK, response.StatusCode)

	var view api.ServiceHealthResponse
	decode(t, response, &view)
	require.Empty(t, view.Services)
	require.NotNil(t, view.Services, "an empty view renders an empty array rather than null")
	require.Equal(t, api.DistributionLocal, view.Distribution.State)

	var health api.HealthResponse
	decode(t, get(t, srv.URL+api.PathHealth), &health)
	require.NotContains(t, health.Checks, api.HealthCheckServiceMonitor)
}
