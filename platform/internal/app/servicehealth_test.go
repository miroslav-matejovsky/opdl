package app

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
