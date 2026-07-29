package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/servicehealth"
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

// logLines decodes what a JSON handler wrote, one record per line.
func logLines(t *testing.T, buffer *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range bytes.Lines(buffer.Bytes()) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record map[string]any
		require.NoError(t, json.Unmarshal(line, &record))
		records = append(records, record)
	}
	return records
}

// TestHealthLogWritesTransitionsAndDropsRepeats checks the sink the monitor
// writes to until distribution lands.
//
// Every attempt reaches it, including the ones that changed nothing, because
// that is what the monitor promises its sink. What a person reading a log wants
// is when something changed, and a probe every few seconds per service would
// otherwise bury that under repeats.
func TestHealthLogWritesTransitionsAndDropsRepeats(t *testing.T) {
	var buffer bytes.Buffer
	sink := newHealthLog(slog.New(slog.NewJSONHandler(&buffer, nil)))

	observe := func(status servicehealth.Status, pending int, failure string) {
		sink.Observed(t.Context(), servicehealth.Observation{
			Service:         "alarm-service",
			Role:            "master",
			Status:          status,
			Latency:         3 * time.Millisecond,
			PendingFailures: pending,
			Error:           failure,
		})
	}

	observe(servicehealth.StatusHealthy, 0, "")
	observe(servicehealth.StatusHealthy, 0, "")
	observe(servicehealth.StatusHealthy, 1, "connection refused")
	observe(servicehealth.StatusUnhealthy, 2, "connection refused")
	observe(servicehealth.StatusUnhealthy, 2, "connection refused")
	observe(servicehealth.StatusHealthy, 0, "")

	records := logLines(t, &buffer)
	require.Len(t, records, 3, "only the three transitions were written: %+v", records)

	require.Equal(t, "service health changed", records[0]["msg"])
	require.Equal(t, "healthy", records[0]["status"])
	require.Equal(t, "master", records[0]["service_role"])
	require.NotContains(t, records[0], "error")

	require.Equal(t, "service is unhealthy", records[1]["msg"],
		"a service that is down is an error record, not an informational one")
	require.Equal(t, "unhealthy", records[1]["status"])
	require.Equal(t, float64(2), records[1]["consecutive_failures"])
	require.Equal(t, "connection refused", records[1]["error"])

	require.Equal(t, "healthy", records[2]["status"])
}

// TestHealthLogTracksEachServiceOnItsOwn checks one service's transitions do not
// suppress another's. The sink is shared by every worker on the machine, so the
// state it keeps has to be per service.
func TestHealthLogTracksEachServiceOnItsOwn(t *testing.T) {
	var buffer bytes.Buffer
	sink := newHealthLog(slog.New(slog.NewJSONHandler(&buffer, nil)))

	sink.Observed(t.Context(), servicehealth.Observation{Service: "alarm-service", Status: servicehealth.StatusHealthy})
	sink.Observed(t.Context(), servicehealth.Observation{Service: "reporting-service", Status: servicehealth.StatusHealthy})
	sink.Observed(t.Context(), servicehealth.Observation{Service: "alarm-service", Status: servicehealth.StatusHealthy})

	records := logLines(t, &buffer)
	require.Len(t, records, 2)
	require.Equal(t, "alarm-service", records[0]["service"])
	require.Equal(t, "reporting-service", records[1]["service"])
}
