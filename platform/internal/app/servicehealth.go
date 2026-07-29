package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/servicehealth"
)

// healthTargets resolves this machine's services into probeable targets.
//
// This is the whole of the adapter between the descriptor and the monitor. The
// machine package parses no deployment strings and imports no configuration, so
// the durations are parsed here and the request URL is assembled here, from the
// machine's own ip and each service's authored port and path.
//
// The ip is the machine's rather than loopback. A service's listener is not the
// platform's: it may be bound on the machine's address, and on a scenario host
// running several logical machines that address is what tells one machine's
// services from another's.
//
// Every value was validated when the descriptor decoded, so a failure here is a
// descriptor and a runtime that disagree rather than an author's mistake. It
// stops the process either way: a machine that cannot probe its services would
// otherwise report every one of them Unknown for as long as it ran.
func healthTargets(descriptor config.Descriptor) ([]servicehealth.Target, error) {
	targets := make([]servicehealth.Target, 0, len(descriptor.Services))
	for _, service := range descriptor.Services {
		check := service.HealthCheck
		interval, err := time.ParseDuration(strings.TrimSpace(check.Interval))
		if err != nil {
			return nil, fmt.Errorf("service %q health_check.interval %q: %w", service.Name, check.Interval, err)
		}
		timeout, err := time.ParseDuration(strings.TrimSpace(check.Timeout))
		if err != nil {
			return nil, fmt.Errorf("service %q health_check.timeout %q: %w", service.Name, check.Timeout, err)
		}
		probeURL, err := healthURL(descriptor.IP, check)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", service.Name, err)
		}
		targets = append(targets, servicehealth.Target{
			Service:  service.Name,
			Role:     service.Role,
			URL:      probeURL,
			Interval: interval,
			Timeout:  timeout,
			Retries:  check.Retries,
		})
	}
	return targets, nil
}

// healthURL joins the machine's ip and a service's authored port and path into
// the URL one attempt fetches.
//
// The path is appended as authored rather than rebuilt through url.URL, because
// url.URL re-encodes what it renders and the authored bytes are the point: a
// service that distinguishes its health endpoints by case, by a query, or by
// which characters are escaped is entitled to be probed at the one its author
// wrote. What makes appending safe is that the descriptor already proved the
// path is a request target and nothing more — it starts with a slash and
// carries no scheme, host, or fragment.
//
// The result is parsed back before it is returned. That is the check appending
// would otherwise skip: it confirms the host and scheme are the ones composed
// here and not something a path smuggled in, so a probe can only ever reach
// this machine on the authored port.
func healthURL(ip string, check config.HealthCheck) (string, error) {
	if check.Type != probeTypeHTTP {
		return "", fmt.Errorf("health_check.type %q has no probe", check.Type)
	}
	if net.ParseIP(strings.TrimSpace(ip)) == nil {
		return "", fmt.Errorf("machine ip %q is not an address a probe can reach", ip)
	}
	host := net.JoinHostPort(strings.TrimSpace(ip), strconv.Itoa(check.Port))
	probe := probeTypeHTTP + "://" + host + check.Path

	parsed, err := url.Parse(probe)
	if err != nil {
		return "", fmt.Errorf("health_check.path %q does not compose a probe URL: %w", check.Path, err)
	}
	if parsed.Scheme != probeTypeHTTP || parsed.Host != host {
		return "", fmt.Errorf("health_check.path %q resolves the probe onto %s://%s rather than this machine",
			check.Path, parsed.Scheme, parsed.Host)
	}
	return probe, nil
}

// probeTypeHTTP is the one probe kind the runtime can run. The descriptor
// rejects any other, so this is what the adapter matches on rather than a
// second list of kinds.
const probeTypeHTTP = "http"

// healthLog reports stable service transitions to the application log.
//
// It is the sink the monitor writes to until distribution lands. Every attempt
// reaches it and only a change is written: a probe every few seconds per
// service would otherwise fill the log with a service that has not moved, and
// what a person reading it wants is when something changed.
//
// Nothing here is a fact in the event sense. Service health is recalculated
// current state that expires, so it never reaches the event record, the machine
// store, or a site journal — see the accepted design in
// docs/backlog/service-health.md.
type healthLog struct {
	log *slog.Logger
	// mu guards last, which every worker writes to. One monitor runs a worker per
	// service and they report concurrently.
	mu   sync.Mutex
	last map[string]servicehealth.Status
}

func newHealthLog(log *slog.Logger) *healthLog {
	return &healthLog{log: log, last: map[string]servicehealth.Status{}}
}

// Observed writes the transitions and drops the repeats.
func (h *healthLog) Observed(_ context.Context, observation servicehealth.Observation) {
	h.mu.Lock()
	previous, seen := h.last[observation.Service]
	changed := !seen || previous != observation.Status
	h.last[observation.Service] = observation.Status
	h.mu.Unlock()

	if !changed {
		return
	}
	attrs := []any{
		"service", observation.Service,
		"service_role", observation.Role,
		"status", string(observation.Status),
		"latency_ms", observation.Latency.Milliseconds(),
	}
	if observation.PendingFailures > 0 {
		attrs = append(attrs, "consecutive_failures", observation.PendingFailures)
	}
	if observation.Error != "" {
		attrs = append(attrs, "error", observation.Error)
	}
	if observation.Status == servicehealth.StatusUnhealthy {
		h.log.Error("service is unhealthy", attrs...)
		return
	}
	h.log.Info("service health changed", attrs...)
}

// startServiceHealth begins probing this machine's services.
//
// It is composed at process lifetime rather than inside an activation, and that
// is the decision this function exists to make. Both of a machine's instances
// probe every service on it, in every ownership state: a service does not stop
// needing to be watched because the process watching it stepped down, and an
// observation from a Passive instance is what keeps a machine's health visible
// while ownership is moving.
//
// Nothing it produces reaches platform health, readiness, or ownership. A
// failed service is a fact about the service, and both instances of the machine
// can see it, so moving the platform's listener would repair nothing and could
// hand a machine back and forth over a target neither instance controls.
func startServiceHealth(ctx context.Context, proc process) (*servicehealth.Monitor, error) {
	targets, err := healthTargets(proc.descriptor)
	if err != nil {
		return nil, err
	}
	return servicehealth.Start(ctx, servicehealth.Deps{
		Prober: servicehealth.NewHTTPProber(),
		Sink:   newHealthLog(proc.log),
		Clock:  servicehealth.SystemClock{},
	}, targets)
}
