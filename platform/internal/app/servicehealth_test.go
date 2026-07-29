package app

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/servicehealth"
	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthview"
)

// The adapter between the descriptor and the monitor is the only place the
// machine's ip meets a service's authored port and path. These tests are about
// what it composes from them, and about the one thing it must never do: let an
// authored path decide where a probe connects.

func healthCheck(port int, path string) config.HealthCheck {
	return config.HealthCheck{
		Type:     "http",
		Port:     port,
		Path:     path,
		Interval: "10s",
		Timeout:  "2s",
		Retries:  3,
	}
}

// TestHealthTargetsResolveEveryHostedService checks each service becomes one
// target, on the machine's own ip, with its authored timings typed.
func TestHealthTargetsResolveEveryHostedService(t *testing.T) {
	descriptor := config.Descriptor{
		Machine: "sensor",
		IP:      "10.0.1.10",
		Services: []config.Service{
			{Name: "alarm-service", Role: "master", HealthCheck: healthCheck(9101, "/health")},
			{Name: "reporting-service", Role: "slave", HealthCheck: healthCheck(9102, "/healthz?deep=1")},
		},
	}

	targets, err := healthTargets(descriptor)
	require.NoError(t, err)
	require.Equal(t, []servicehealth.Target{
		{
			Service:  "alarm-service",
			Role:     "master",
			URL:      "http://10.0.1.10:9101/health",
			Interval: 10 * time.Second,
			Timeout:  2 * time.Second,
			Retries:  3,
		},
		{
			Service:  "reporting-service",
			Role:     "slave",
			URL:      "http://10.0.1.10:9102/healthz?deep=1",
			Interval: 10 * time.Second,
			Timeout:  2 * time.Second,
			Retries:  3,
		},
	}, targets)
}

// TestHealthTargetsProbeTheMachineIPRatherThanLoopback pins the one address
// decision here.
//
// The platform's own API is machine-local and resolved onto loopback. A service
// listener is not the platform's, and on a scenario host running several
// logical machines the machine ip is what tells one machine's services from
// another's.
func TestHealthTargetsProbeTheMachineIPRatherThanLoopback(t *testing.T) {
	targets, err := healthTargets(config.Descriptor{
		Machine:  "sensor",
		IP:       "10.0.1.11",
		Services: []config.Service{{Name: "alarm-service", Role: "master", HealthCheck: healthCheck(9101, "/health")}},
	})
	require.NoError(t, err)
	require.Equal(t, "http://10.0.1.11:9101/health", targets[0].URL)
}

// TestHealthURLKeepsTheAuthoredPath checks the path reaches the probe exactly as
// it was written.
//
// It travelled from the blueprint to the descriptor byte for byte, and a service
// that distinguishes its endpoints by case, by a query, or by which characters
// are escaped is entitled to be probed at the one its author wrote. Rebuilding
// the URL through url.URL would re-encode it.
func TestHealthURLKeepsTheAuthoredPath(t *testing.T) {
	paths := []string{
		"/health",
		"/Health/Ready",
		"/health?verbose=1",
		"/health?deep=1&timeout=2s",
		"/health/%20spaced",
		"/health?",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			probe, err := healthURL("10.0.1.10", healthCheck(9101, path))
			require.NoError(t, err)
			require.Equal(t, "http://10.0.1.10:9101"+path, probe)
		})
	}
}

// TestHealthURLCannotBeMovedByItsPath is why appending the path is safe.
//
// The authority is already terminated by the port when the path is appended, so
// a path that looks like it names a host is parsed as a path and the probe still
// reaches this machine. The descriptor rejects such a path before it ever gets
// here; this pins the structural reason rather than relying on that alone,
// because this is the one place a configured string decides what a process
// connects to.
func TestHealthURLCannotBeMovedByItsPath(t *testing.T) {
	probe, err := healthURL("10.0.1.10", healthCheck(9101, "//evil.example.com/health"))
	require.NoError(t, err)
	require.Equal(t, "http://10.0.1.10:9101//evil.example.com/health", probe)

	parsed, err := url.Parse(probe)
	require.NoError(t, err)
	require.Equal(t, "10.0.1.10:9101", parsed.Host,
		"what follows the port is a path, whatever it looks like")
	require.Equal(t, "//evil.example.com/health", parsed.Path)
}

// TestHealthURLRefusesAPathThatDoesNotCompose checks a path that will not parse
// stops the process rather than becoming a probe nothing can send.
func TestHealthURLRefusesAPathThatDoesNotCompose(t *testing.T) {
	_, err := healthURL("10.0.1.10", healthCheck(9101, "/health%zz"))
	require.ErrorContains(t, err, "does not compose a probe URL")
}

// TestHealthTargetsRejectAnUnusableDescriptor checks a machine that cannot probe
// its services stops rather than reporting every one of them Unknown for as
// long as it runs.
func TestHealthTargetsRejectAnUnusableDescriptor(t *testing.T) {
	tests := map[string]struct {
		descriptor config.Descriptor
		errText    string
	}{
		"unknown probe type": {
			descriptor: config.Descriptor{IP: "10.0.1.10", Services: []config.Service{
				{Name: "alarm-service", Role: "master", HealthCheck: config.HealthCheck{
					Type: "ping", Port: 9101, Path: "/health", Interval: "10s", Timeout: "2s", Retries: 3,
				}},
			}},
			errText: `type "ping" has no probe`,
		},
		"machine ip that is not an address": {
			descriptor: config.Descriptor{IP: "sensor.local", Services: []config.Service{
				{Name: "alarm-service", Role: "master", HealthCheck: healthCheck(9101, "/health")},
			}},
			errText: "is not an address a probe can reach",
		},
		"unparseable interval": {
			descriptor: config.Descriptor{IP: "10.0.1.10", Services: []config.Service{
				{Name: "alarm-service", Role: "master", HealthCheck: config.HealthCheck{
					Type: "http", Port: 9101, Path: "/health", Interval: "soon", Timeout: "2s", Retries: 3,
				}},
			}},
			errText: "health_check.interval",
		},
		"unparseable timeout": {
			descriptor: config.Descriptor{IP: "10.0.1.10", Services: []config.Service{
				{Name: "alarm-service", Role: "master", HealthCheck: config.HealthCheck{
					Type: "http", Port: 9101, Path: "/health", Interval: "10s", Timeout: "soon", Retries: 3,
				}},
			}},
			errText: "health_check.timeout",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := healthTargets(test.descriptor)
			require.ErrorContains(t, err, test.errText)
			require.ErrorContains(t, err, "alarm-service", "the error names the service to fix")
		})
	}
}

// TestHealthInventoryResolvesTheSiteAsBuilt checks the remote half of the
// descriptor becomes what a view is built from.
//
// The freshness is the descriptor's exact value rather than one recomputed
// here. Every machine of the site was built with the same number, and a receiver
// that derived its own would expire reports at a different age from its peers.
func TestHealthInventoryResolvesTheSiteAsBuilt(t *testing.T) {
	units, err := healthInventory(config.Descriptor{
		SiteServices: []config.SiteService{
			{
				Machine: "sensor", MachineProfile: "sensor-node",
				Service: "alarm-service", ServiceRole: "master",
				ObserverRoles: []string{"primary", "standby"}, FreshFor: "22s",
			},
			{
				Machine: "gateway", MachineProfile: "gateway-node",
				Service: "gateway-services", ServiceRole: "slave",
				ObserverRoles: []string{"primary"}, FreshFor: "32s",
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []healthview.Unit{
		{
			UnitKey:        healthview.UnitKey{Machine: "sensor", Service: "alarm-service"},
			MachineProfile: "sensor-node", ServiceRole: "master",
			ObserverRoles: []string{"primary", "standby"}, FreshFor: 22 * time.Second,
		},
		{
			UnitKey:        healthview.UnitKey{Machine: "gateway", Service: "gateway-services"},
			MachineProfile: "gateway-node", ServiceRole: "slave",
			ObserverRoles: []string{"primary"}, FreshFor: 32 * time.Second,
		},
	}, units)
}

// TestHealthInventoryRejectsAnUnusableFreshness checks a process whose site
// inventory cannot be read stops rather than running with a view it cannot
// expire anything in.
func TestHealthInventoryRejectsAnUnusableFreshness(t *testing.T) {
	_, err := healthInventory(config.Descriptor{
		SiteServices: []config.SiteService{{
			Machine: "sensor", MachineProfile: "sensor-node",
			Service: "alarm-service", ServiceRole: "master",
			ObserverRoles: []string{"primary"}, FreshFor: "soon",
		}},
	})
	require.ErrorContains(t, err, "sensor/alarm-service fresh_for")
}

// TestHealthInventoryDoesNotAliasTheDescriptor checks the observer roles a view
// holds are its own.
//
// The descriptor is shared by everything in the process, and a view that kept a
// slice from it would let a later reader mutate what the reduction depends on.
func TestHealthInventoryDoesNotAliasTheDescriptor(t *testing.T) {
	descriptor := config.Descriptor{
		SiteServices: []config.SiteService{{
			Machine: "sensor", MachineProfile: "sensor-node",
			Service: "alarm-service", ServiceRole: "master",
			ObserverRoles: []string{"primary", "standby"}, FreshFor: "22s",
		}},
	}
	units, err := healthInventory(descriptor)
	require.NoError(t, err)

	descriptor.SiteServices[0].ObserverRoles[0] = "tampered"
	require.Equal(t, []string{"primary", "standby"}, units[0].ObserverRoles)
}

// TestLocalSinkAppliesToItsOwnViewAndPublishes checks what this machine found
// reaches both places it has to.
//
// The local view is written whether or not the site ever hears about it, so a
// machine whose broker is unreachable still answers correctly about its own
// services. The stamped sequence is the publisher's, so this instance's own slot
// is ordered by the same numbers its peers receive.
func TestLocalSinkAppliesToItsOwnViewAndPublishes(t *testing.T) {
	view, err := healthview.New(
		healthview.Deployment{Project: "customer-a", Environment: "production", Site: "north"},
		[]healthview.Unit{{
			UnitKey:        healthview.UnitKey{Machine: "sensor", Service: "alarm-service"},
			MachineProfile: "sensor-node", ServiceRole: "master",
			ObserverRoles: []string{"primary", "standby"}, FreshFor: 22 * time.Second,
		}},
		healthview.SystemClock{},
	)
	require.NoError(t, err)

	conn := &countingConn{}
	publisher, err := healthfabric.NewPublisher(conn, healthfabric.Identity{
		Deployment:   view.Deployment(),
		Machine:      "sensor",
		ObserverRole: "standby",
		Epoch:        3,
	}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	defer publisher.Close()

	sink := &localSink{
		machine:   "sensor",
		role:      "standby",
		view:      view,
		publisher: publisher,
		log:       slog.New(slog.DiscardHandler),
	}
	sink.Observed(t.Context(), servicehealth.Observation{
		Service:         "alarm-service",
		Role:            "master",
		Status:          servicehealth.StatusUnhealthy,
		CheckedAt:       time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Latency:         3 * time.Millisecond,
		PendingFailures: 2,
		Error:           "connection refused",
	})

	unit := view.Snapshot().Units[0]
	require.Equal(t, healthview.StatusUnhealthy, unit.Status)
	require.Equal(t, []string{"primary"}, unit.MissingObservers,
		"this instance reported; its peer has not")
	require.Len(t, unit.Observations, 1)
	require.Equal(t, "standby", unit.Observations[0].ObserverRole)
	require.Equal(t, 2, unit.Observations[0].ConsecutiveFailures)
	require.Equal(t, "connection refused", unit.Observations[0].Error)

	require.Eventually(t, func() bool {
		return publisher.Counters().Published == 1
	}, 5*time.Second, 5*time.Millisecond, "the same observation went to the site")
}

// countingConn accepts everything and remembers nothing. The wire contract is
// tested in healthfabric; what matters here is that the sink publishes at all.
type countingConn struct{}

func (*countingConn) Publish(string, []byte) error { return nil }
func (*countingConn) Subscribe(string, func([]byte)) (healthfabric.Subscription, error) {
	return nil, errors.New("this connection does not subscribe")
}
func (*countingConn) Flush(context.Context) error { return nil }

// listeningConn accepts a subscription as well, which the rendering tests need:
// a rendered response reports the subscriber's tallies beside the view's.
type listeningConn struct{ countingConn }

func (*listeningConn) Subscribe(string, func([]byte)) (healthfabric.Subscription, error) {
	return noSubscription{}, nil
}

type noSubscription struct{}

func (noSubscription) Unsubscribe() error { return nil }

// siteOf builds the two-machine site the rendering tests read: this machine's
// service and its neighbour's, in that order, so what the response preserves
// about inventory order is visible.
func siteOf() []healthview.Unit {
	return []healthview.Unit{
		{
			UnitKey:        healthview.UnitKey{Machine: "sensor", Service: "alarm-service"},
			MachineProfile: "sensor-node", ServiceRole: "master",
			ObserverRoles: []string{"primary", "standby"}, FreshFor: 22 * time.Second,
		},
		{
			UnitKey:        healthview.UnitKey{Machine: "gateway", Service: "reader-service"},
			MachineProfile: "gateway-node", ServiceRole: "slave",
			ObserverRoles: []string{"primary"}, FreshFor: 22 * time.Second,
		},
	}
}

// composed assembles the subsystem an endpoint renders from, without a broker
// or a monitor behind it. Response reads the view and the two fabric halves and
// nothing else, so those are the only parts it needs.
func composed(t *testing.T, inventory []healthview.Unit) *serviceHealth {
	t.Helper()
	deployment := healthview.Deployment{Project: "customer-a", Environment: "production", Site: "north"}
	view, err := healthview.New(deployment, inventory, healthview.SystemClock{})
	require.NoError(t, err)

	conn := &listeningConn{}
	quiet := slog.New(slog.DiscardHandler)
	subscriber, err := healthfabric.Subscribe(t.Context(), conn, view, quiet)
	require.NoError(t, err)
	t.Cleanup(subscriber.Close)

	publisher, err := healthfabric.NewPublisher(conn, healthfabric.Identity{
		Deployment: deployment, Machine: "sensor", ObserverRole: "primary", Epoch: 4,
	}, quiet)
	require.NoError(t, err)
	t.Cleanup(publisher.Close)

	return &serviceHealth{
		machine: "sensor", role: "primary",
		view: view, subscriber: subscriber, publisher: publisher,
	}
}

// found is the primary observer's report about one service, as it would arrive
// from the fabric. Every unit below expects a primary, and only some expect a
// standby, so this is the one observer any of them can be told about.
func found(machine, service string, status healthview.Status, sequence uint64) healthview.Observation {
	return healthview.Observation{
		Unit:         healthview.UnitKey{Machine: machine, Service: service},
		ObserverRole: "primary",
		Epoch:        4,
		Sequence:     sequence,
		Status:       status,
		CheckedAtUTC: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Latency:      7 * time.Millisecond,
	}
}

// TestResponseRendersWhatThisInstanceKnows is the endpoint's whole contract: the
// snapshot, rendered, with this instance named as the one that produced it.
//
// Two instances of one machine hold separate views and may briefly differ, so a
// response that did not say whose it was could not be compared with another.
func TestResponseRendersWhatThisInstanceKnows(t *testing.T) {
	health := composed(t, siteOf())
	require.Equal(t, healthview.DropNone,
		health.view.Apply(found("sensor", "alarm-service", healthview.StatusHealthy, 1)))

	response := health.Response()

	require.Equal(t, "customer-a", response.Project)
	require.Equal(t, "production", response.Environment)
	require.Equal(t, "north", response.Site)
	require.Equal(t, "sensor", response.Machine)
	require.Equal(t, "primary", response.Role)
	require.Equal(t, api.ServiceHealthSummary{Healthy: 1, Unknown: 1}, response.Summary)

	require.Len(t, response.Services, 2)
	require.Equal(t, "alarm-service", response.Services[0].Service, "inventory order is preserved")
	require.Equal(t, "reader-service", response.Services[1].Service)

	reported := response.Services[0]
	require.Equal(t, api.ServiceStatusHealthy, reported.Status)
	require.Equal(t, "sensor-node", reported.MachineProfile)
	require.Equal(t, "master", reported.ServiceRole)
	require.Equal(t, []string{"primary", "standby"}, reported.ExpectedObservers)
	require.Equal(t, []string{"standby"}, reported.MissingObservers, "the peer has not reported")
	require.Empty(t, reported.StaleObservers)
	require.Len(t, reported.Observations, 1)
	require.Equal(t, "primary", reported.Observations[0].ObserverRole)
	require.Equal(t, api.ServiceStatusHealthy, reported.Observations[0].Status)
	require.False(t, reported.Observations[0].Stale)
	require.Equal(t, "2026-07-29T12:00:00Z", reported.Observations[0].CheckedAtUTC)
	require.Equal(t, int64(7), reported.Observations[0].LatencyMs)

	silent := response.Services[1]
	require.Equal(t, api.ServiceStatusUnknown, silent.Status,
		"a service nothing has reported on is Unknown rather than absent")
	require.Equal(t, []string{"primary"}, silent.MissingObservers)
	require.Empty(t, silent.Observations)
}

// TestResponseRendersAFindingInThePublishedVocabulary keeps the two status sets
// apart. The view's are wire and reducer values; the API's are a published
// contract, and passing one through where the other is meant is exactly what a
// single mapping in one place prevents.
func TestResponseRendersAFindingInThePublishedVocabulary(t *testing.T) {
	health := composed(t, siteOf())
	health.view.Apply(found("sensor", "alarm-service", healthview.StatusUnhealthy, 1))

	unit := health.Response().Services[0]
	require.Equal(t, api.ServiceStatusUnhealthy, unit.Status)
	require.Equal(t, api.ServiceStatusUnhealthy, unit.Observations[0].Status)
	require.Equal(t, api.ServiceHealthSummary{Unhealthy: 1, Unknown: 1}, health.Response().Summary)
}

// TestResponseReportsEveryDropReasonEvenAtZero keeps the counters readable. A
// row that appears only once it is nonzero makes an operator prove a reason
// exists before they can see it is not happening.
func TestResponseReportsEveryDropReasonEvenAtZero(t *testing.T) {
	health := composed(t, siteOf())
	require.Equal(t, healthview.DropUnknownTarget,
		health.view.Apply(found("sensor", "no-such-service", healthview.StatusHealthy, 1)))

	dropped := health.Response().Distribution.Dropped
	reasons := make([]string, 0, len(dropped))
	counts := map[string]int64{}
	for _, drop := range dropped {
		reasons = append(reasons, drop.Reason)
		counts[drop.Reason] = drop.Count
	}
	require.Equal(t, []string{"duplicate", "stale", "unknown_observer", "unknown_target", "unusable_status"}, reasons,
		"sorted by reason, so a poller sees a row move only when its number does")
	require.Equal(t, int64(1), counts["unknown_target"])
	require.Equal(t, int64(0), counts["stale"])

	rejected := make([]string, 0)
	for _, reject := range health.Response().Distribution.Rejected {
		rejected = append(rejected, reject.Reason)
		require.Equal(t, int64(0), reject.Count, "nothing malformed was delivered here")
	}
	require.Equal(t, []string{"oversize", "malformed", "version", "foreign_deployment", "incomplete", "impossible"},
		rejected, "the fabric's own fixed order")
}

// TestDistributionStateAsksOnlyAboutOtherMachines is what makes this state worth
// reading. This instance's own observations reach its view directly, so counting
// them would report a working site through a broker that had stopped carrying
// anything.
func TestDistributionStateAsksOnlyAboutOtherMachines(t *testing.T) {
	health := composed(t, siteOf())
	health.view.Apply(found("sensor", "alarm-service", healthview.StatusHealthy, 1))
	require.Equal(t, api.DistributionIsolated, health.Response().Distribution.State,
		"this machine is reporting; nothing from the rest of the site has arrived")

	health.view.Apply(found("gateway", "reader-service", healthview.StatusHealthy, 2))
	require.Equal(t, api.DistributionConnected, health.Response().Distribution.State)
}

// TestDistributionStateIsLocalWithNoRemoteObserversToHearFrom keeps the state
// honest on a one-machine site. There is no traffic there whose absence would
// mean anything, and calling that Connected would claim a working fabric on no
// evidence at all.
func TestDistributionStateIsLocalWithNoRemoteObserversToHearFrom(t *testing.T) {
	health := composed(t, siteOf()[:1])
	require.Equal(t, api.DistributionLocal, health.Response().Distribution.State)
}

// TestDistributionStateIsPartialWhenSomeOfTheSiteIsReporting covers the middle
// case: enough is arriving to prove the fabric works, and not all of it.
func TestDistributionStateIsPartialWhenSomeOfTheSiteIsReporting(t *testing.T) {
	inventory := append(siteOf(), healthview.Unit{
		UnitKey:        healthview.UnitKey{Machine: "gateway", Service: "writer-service"},
		MachineProfile: "gateway-node", ServiceRole: "master",
		ObserverRoles: []string{"primary"}, FreshFor: 22 * time.Second,
	})
	health := composed(t, inventory)
	health.view.Apply(found("gateway", "reader-service", healthview.StatusHealthy, 1))

	require.Equal(t, api.DistributionPartial, health.Response().Distribution.State)
}
