package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/servicehealth"
	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthfabric"
	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthview"
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

// healthInventory resolves the site's static health inventory into what a view
// is built from.
//
// Every machine of the site carries the same list in the same order, so a view
// built from it renders the same way on every instance. The freshness is the
// descriptor's exact value rather than one derived here: a receiver that
// computed its own would expire reports at a different age from its peers, and
// the two would disagree about a service neither had a problem with.
func healthInventory(descriptor config.Descriptor) ([]healthview.Unit, error) {
	units := make([]healthview.Unit, 0, len(descriptor.SiteServices))
	for _, unit := range descriptor.SiteServices {
		freshFor, err := time.ParseDuration(strings.TrimSpace(unit.FreshFor))
		if err != nil {
			return nil, fmt.Errorf("site service %s/%s fresh_for %q: %w", unit.Machine, unit.Service, unit.FreshFor, err)
		}
		units = append(units, healthview.Unit{
			UnitKey:        healthview.UnitKey{Machine: unit.Machine, Service: unit.Service},
			MachineProfile: unit.MachineProfile,
			ServiceRole:    unit.ServiceRole,
			ObserverRoles:  slices.Clone(unit.ObserverRoles),
			FreshFor:       freshFor,
		})
	}
	return units, nil
}

// localSink applies this instance's own observations to its view and hands them
// to the publisher.
//
// The local view is written before the observation is queued, so this instance
// knows what it found whether or not the site ever hears about it. A machine
// whose broker is unreachable still answers correctly about its own services,
// which is the answer an operator standing in front of it most needs.
type localSink struct {
	machine   string
	role      string
	view      *healthview.View
	publisher *healthfabric.Publisher
	log       *slog.Logger
}

// Observed folds one attempt into the local view and sends it on.
func (s *localSink) Observed(_ context.Context, observation servicehealth.Observation) {
	report := healthview.Observation{
		Unit:                healthview.UnitKey{Machine: s.machine, Service: observation.Service},
		ObserverRole:        s.role,
		Status:              healthview.Status(observation.Status),
		CheckedAtUTC:        observation.CheckedAt.UTC(),
		Latency:             observation.Latency,
		ConsecutiveFailures: observation.PendingFailures,
		Error:               observation.Error,
	}
	// The publisher stamps the epoch and the sequence and hands the stamped value
	// back, so this instance's own slot is ordered by the same numbers its peers
	// receive. It also means the local view is written whether or not the
	// publication ever reaches the broker.
	if dropped := s.view.Apply(s.publisher.Publish(report)); dropped != healthview.DropNone {
		// This is its own machine's observation about its own service, so a drop
		// means the descriptor's two halves disagree in a way validation missed.
		s.log.Warn("this instance's own observation was not applied to its view",
			"service", observation.Service, "reason", string(dropped))
	}
}

// serviceHealth is everything one process runs to watch its machine's services
// and to know what the rest of the site found.
//
// It is composed at process lifetime rather than inside an activation, and that
// is the decision this type exists to make. Both of a machine's instances probe
// every service on it, in every ownership state: a service does not stop needing
// to be watched because the process watching it stepped down, and an observation
// from a Passive instance is what keeps a machine's health visible while
// ownership is moving.
//
// Nothing it produces reaches platform health, readiness, or ownership. A failed
// service is a fact about the service, and both instances of the machine can see
// it, so moving the platform's listener would repair nothing and could hand a
// machine back and forth over a target neither instance controls.
type serviceHealth struct {
	view       *healthview.View
	monitor    *servicehealth.Monitor
	subscriber *healthfabric.Subscriber
	publisher  *healthfabric.Publisher
	conn       *healthfabric.NATSConn
}

// startServiceHealth brings up this process's service monitoring and its half of
// the site's health traffic.
//
// The order is the point. The view exists before anything can write to it; the
// subscription is established before the first local probe, so nothing another
// instance says in between is missed — there is no replay to recover it with;
// and probing starts last, once there is somewhere for its results to go.
//
// A failure at any step unwinds what came before it. A process that cannot watch
// its services stops rather than running blind, because the alternative is a
// deployment reporting every service Unknown with nothing to say why.
func startServiceHealth(ctx context.Context, proc process, broker healthfabric.InProcessConnProvider, epoch uint64) (*serviceHealth, error) {
	descriptor := proc.descriptor
	targets, err := healthTargets(descriptor)
	if err != nil {
		return nil, err
	}
	inventory, err := healthInventory(descriptor)
	if err != nil {
		return nil, err
	}
	deployment := healthview.Deployment{
		Project:     descriptor.Project,
		Environment: descriptor.Environment,
		Site:        descriptor.Site,
	}
	view, err := healthview.New(deployment, inventory, healthview.SystemClock{})
	if err != nil {
		return nil, err
	}

	// A second connection to the same broker, beside the event fabric's. The two
	// carry different traffic under different rules — durable facts against
	// expiring current state — and a health subscription being torn down must not
	// disturb the connection the health endpoint round-trips on.
	conn, err := healthfabric.Connect(broker, fabricClientName(descriptor, proc.role)+"-health")
	if err != nil {
		return nil, err
	}
	subscriber, err := healthfabric.Subscribe(ctx, conn, view, proc.log)
	if err != nil {
		conn.Close()
		return nil, err
	}
	publisher, err := healthfabric.NewPublisher(conn, healthfabric.Identity{
		Deployment:   deployment,
		Machine:      descriptor.Machine,
		ObserverRole: proc.role.String(),
		// The durable instance epoch captured after the process-start advance, so
		// a restarted observer's first report supersedes everything its previous
		// incarnation said. Later activation advances do not change this captured
		// value: ownership moves without changing the observer process.
		Epoch: epoch,
	}, proc.log)
	if err != nil {
		subscriber.Close()
		conn.Close()
		return nil, err
	}
	monitor, err := servicehealth.Start(ctx, servicehealth.Deps{
		Prober: servicehealth.NewHTTPProber(),
		Sink: &localSink{
			machine:   descriptor.Machine,
			role:      proc.role.String(),
			view:      view,
			publisher: publisher,
			log:       proc.log,
		},
		Clock: servicehealth.SystemClock{},
	}, targets)
	if err != nil {
		publisher.Close()
		subscriber.Close()
		conn.Close()
		return nil, err
	}
	proc.log.Info("service health started",
		"targets", len(targets), "site_units", len(inventory), "subject", healthfabric.Subject)
	return &serviceHealth{view: view, monitor: monitor, subscriber: subscriber, publisher: publisher, conn: conn}, nil
}

// Stop shuts monitoring down in the reverse of the order it came up.
//
// Probing stops first, so nothing new is produced; then the publisher, which
// drops whatever it was still holding rather than flushing it — an observation
// from a process that is stopping is about to be superseded by nothing at all,
// and the site should see its silence. The subscription and the connection go
// last, once nothing is writing to either.
func (s *serviceHealth) Stop() {
	s.monitor.Stop()
	s.publisher.Close()
	s.subscriber.Close()
	s.conn.Close()
}

// The view has no reader yet. GET /health/services is the next piece of this
// feature and is what will expose it; until then the view is built, converged,
// and expired exactly as it will be then, which is what makes adding the
// endpoint a matter of rendering a snapshot rather than of building one.
