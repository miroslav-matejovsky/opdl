package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

const (
	apiTitle       = "OPDL Platform API"
	apiVersion     = "0.1.0"
	apiDescription = "HTTP API served by the OPDL platform runtime."
)

// The paths the platform serves. They are exported because a Passive instance
// serves the same API surface without the domain behavior behind it: it answers
// PathInstance and refuses every domain path. Naming them once is what keeps
// that refusal list from drifting away from the operations registered below.
const (
	// PathInstance is the instance's own identity and state. Every instance
	// answers it in every state.
	PathInstance = "/instance"
	// PathHealth is the primary operational health endpoint.
	PathHealth = "/health"
	// PathHealthLive is the process liveness endpoint.
	PathHealthLive = "/health/live"
	// PathHealthReady is the operational readiness endpoint.
	PathHealthReady = "/health/ready"
	// PathHealthHA is the redundancy and ownership diagnostics endpoint.
	PathHealthHA = "/health/ha"
	// PathHealthServices is this instance's view of the site's deployed services.
	// It is not a domain path: it reads only what this process holds in memory, so
	// it needs neither Primary Ownership nor a site projection, and an instance
	// that refuses every domain operation still answers it.
	PathHealthServices = "/health/services"
)

// DomainPaths are the operations that need this node's authoritative projection,
// and which therefore only an Active instance can answer. Each entry is a Go 1.22
// ServeMux pattern: method and path.
//
// A Passive instance registers these to refuse them. It refuses rather than
// omitting them so that a caller that reached the wrong instance is told so, and
// told where to go, instead of getting the 404 an unrouted path would produce.
//
// Empty for now: the platform has no domain operation. It is populated again
// once a domain surface exists.
var DomainPaths []string

// Handlers is what the operations need to serve requests. Spec generation
// leaves it zero: the handler closures are never called during generation, only
// their input and output types are read to build the OpenAPI document.
type Handlers struct {
	// Instance reports what this instance is and what it is doing. It is read on
	// every request rather than captured once, because State changes when Primary
	// Ownership moves.
	Instance func() Instance
	// Health reports primary operational health information.
	//
	// It takes the request's context because it is the one health operation that
	// probes a dependency, and a probe needs something to bound and cancel it.
	// The other three answer from what the process already knows.
	Health func(context.Context) HealthResponse
	// HealthLive reports process liveness information.
	HealthLive func() HealthLiveResponse
	// HealthReady reports operational readiness information.
	HealthReady func() HealthReadyResponse
	// HealthHA reports high-availability and ownership diagnostics.
	HealthHA func() HealthHAResponse
	// HealthServices reports this instance's view of the site's deployed
	// services. It takes no context because it reads memory: the view is built
	// from reports that already arrived, and answering it never waits on anything.
	HealthServices func() ServiceHealthResponse
}

// Deps is what the health operations read the running process through.
//
// Every field is read on each request rather than captured once, because all of
// them change while the process runs: Role and State change when Primary
// Ownership moves, the lease expires and is renewed, the fabric breaks and
// recovers, and the service view changes on every probe interval at the site.
//
// The func fields are optional. A nil one means this build has nothing behind
// that answer, and each operation says so by omitting what it cannot report
// rather than by inventing a healthy value. Spec generation and the boundary
// tests compose a Deps with none of them.
type Deps struct {
	// Instance reports what this instance is and what it is doing.
	Instance func() Instance
	// Started is when the process began running, the origin uptime counts from.
	Started time.Time
	// Lease reports this instance's Primary Ownership. Nil derives ownership from
	// the runtime state alone.
	Lease func() LeaseView
	// EventFabric round-trips a message through this instance's embedded broker
	// and reports what happened. Nil reports no fabric check rather than a healthy
	// one.
	EventFabric func(context.Context) error
	// ServiceHealth renders this instance's current view of the site's services.
	// Nil reports no service monitor check and answers the services endpoint with
	// an empty view.
	ServiceHealth func() ServiceHealthResponse
}

// Config returns the huma configuration for the platform API. It clears the
// schema-link create hook that DefaultConfig installs: that hook injects a
// "$schema" property into every model, which pollutes the generated spec and SDK.
func Config() huma.Config {
	cfg := huma.DefaultConfig(apiTitle, apiVersion)
	cfg.Info.Description = apiDescription
	cfg.CreateHooks = nil
	return cfg
}

type instanceOutput struct {
	Body Instance
}

type healthOutput struct {
	Body HealthResponse
}

type healthLiveOutput struct {
	Body HealthLiveResponse
}

type healthReadyOutput struct {
	Body HealthReadyResponse
}

type healthHAOutput struct {
	Body HealthHAResponse
}

type healthServicesOutput struct {
	Body ServiceHealthResponse
}

// RegisterInstance attaches the instance operation to hapi.
//
// It is separate from Register because it is the one operation that does not
// depend on being Active: a Passive instance serves this and nothing else, so it
// registers this alone. Register calls it, so an Active instance serves the same
// operation on the same path with the same shape, and the generated
// specification describes one endpoint rather than two.
func RegisterInstance(hapi huma.API, instance func() Instance) {
	huma.Register(hapi, huma.Operation{
		OperationID: "getInstance",
		Method:      http.MethodGet,
		Path:        PathInstance,
		Summary:     "Report this instance's identity and state",
		Description: "Answered by every instance in every state, including a Passive one that refuses every domain operation.",
	}, func(_ context.Context, _ *struct{}) (*instanceOutput, error) {
		return &instanceOutput{Body: instance()}, nil
	})
}

// LeaseView is what the runtime tells the health API about this instance's
// Primary Ownership. The runtime fills it from the redundancy package's lease so
// the api package need not depend on it.
type LeaseView struct {
	// Owned reports whether this instance currently holds the lease.
	Owned bool
	// ExpirationUTC is when the lease lapses, an ISO-8601 UTC timestamp, nil when
	// this instance holds no lease or is Active by construction.
	ExpirationUTC *string
}

// NewHealth builds the health handler funcs the runtime serves, deriving each
// response from what deps reports about the running process.
//
// A failing dependency makes the instance Degraded, not Unhealthy. Unhealthy on
// this endpoint is the gate a Passive instance promotes through, and moving
// Primary Ownership would not fix a broken event fabric or a stalled monitor:
// the other instance runs its own broker, its own client, and its own probes, so
// it has nothing better to offer. The instance stays the machine's serving
// instance and says what is wrong with it.
func NewHealth(deps Deps) Handlers {
	return Handlers{
		Health: func(ctx context.Context) HealthResponse {
			inst := deps.Instance()
			checks := map[string]string{HealthCheckConfiguration: HealthStatusHealthy}
			status := HealthStatusHealthy
			if deps.EventFabric != nil {
				checks[HealthCheckEventFabric] = HealthStatusHealthy
				if err := deps.EventFabric(ctx); err != nil {
					checks[HealthCheckEventFabric] = HealthStatusUnhealthy
					status = HealthStatusDegraded
				}
			}
			if deps.ServiceHealth != nil {
				checks[HealthCheckServiceMonitor] = serviceMonitorStatus(deps.ServiceHealth())
				if checks[HealthCheckServiceMonitor] != HealthStatusHealthy {
					status = HealthStatusDegraded
				}
			}
			return HealthResponse{
				Status:       status,
				InstanceID:   inst.Role,
				Role:         inst.Role,
				RuntimeState: inst.State,
				Version:      apiVersion,
				Uptime:       humanizeUptime(time.Since(deps.Started)),
				Checks:       checks,
			}
		},
		HealthLive: func() HealthLiveResponse {
			return HealthLiveResponse{Status: HealthStatusHealthy}
		},
		HealthReady: func() HealthReadyResponse {
			return HealthReadyResponse{Status: HealthStatusHealthy}
		},
		HealthHA: func() HealthHAResponse {
			inst := deps.Instance()
			// Without a lease view, fall back to the runtime state: an Active
			// instance owns, a Passive one does not.
			view := LeaseView{Owned: inst.State == InstanceStateActive}
			if deps.Lease != nil {
				view = deps.Lease()
			}
			leaseState := LeaseStateUnowned
			if view.Owned {
				leaseState = LeaseStateOwned
			}
			return HealthHAResponse{
				Role:               inst.Role,
				RuntimeState:       inst.State,
				LeaseState:         leaseState,
				LeaseExpirationUTC: view.ExpirationUTC,
			}
		},
		HealthServices: deps.ServiceHealth,
	}
}

// serviceMonitorStatus reports this instance's own watching of its machine's
// services, from the view it just rendered.
//
// The question it answers is whether this process is still producing
// observations, not what those observations found. Both are visible in the same
// snapshot, and only one of them belongs in platform health: a target that is
// down is down for the machine's other instance too, so surfacing it here would
// tell an operator to fail over and repair nothing.
//
// What counts as a stalled monitor is this instance's own reports about its own
// machine having expired. An expected observer that has never reported is not
// counted: a process whose monitor could not start does not get this far, so the
// only thing "never" can mean here is "not yet", during the first interval after
// startup.
func serviceMonitorStatus(view ServiceHealthResponse) string {
	for _, unit := range view.Services {
		if unit.Machine != view.Machine {
			continue
		}
		if slices.Contains(unit.StaleObservers, view.Role) {
			return HealthStatusUnhealthy
		}
	}
	return HealthStatusHealthy
}

// humanizeUptime renders a process uptime as "<d>d <hh>h <mm>m <ss>s".
func humanizeUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	d -= minutes * time.Minute
	seconds := d / time.Second
	return fmt.Sprintf("%dd %02dh %02dm %02ds", days, hours, minutes, seconds)
}

// RegisterHealth attaches the health operations to hapi.
func RegisterHealth(hapi huma.API, h Handlers) {
	huma.Register(hapi, huma.Operation{
		OperationID: "getHealth",
		Method:      http.MethodGet,
		Path:        PathHealth,
		Summary:     "Report primary operational health",
		Description: "Overall health assessment, operational status, dependency status, readiness, and basic redundancy visibility.",
	}, func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		if h.Health != nil {
			return &healthOutput{Body: h.Health(ctx)}, nil
		}
		inst := Instance{Role: InstanceRolePrimary, State: InstanceStateActive}
		if h.Instance != nil {
			inst = h.Instance()
		}
		role := inst.Role
		if role == "" {
			role = InstanceRolePrimary
		}
		state := inst.State
		if state == "" {
			state = InstanceStateActive
		}
		return &healthOutput{
			Body: HealthResponse{
				Status:       HealthStatusHealthy,
				InstanceID:   role,
				Role:         role,
				RuntimeState: state,
				Version:      apiVersion,
				Uptime:       "0s",
				Checks:       map[string]string{HealthCheckConfiguration: HealthStatusHealthy},
			},
		}, nil
	})

	huma.Register(hapi, huma.Operation{
		OperationID: "getHealthLive",
		Method:      http.MethodGet,
		Path:        PathHealthLive,
		Summary:     "Report process liveness",
		Description: "Lightweight check determining whether the process and main execution loop are running.",
	}, func(_ context.Context, _ *struct{}) (*healthLiveOutput, error) {
		if h.HealthLive != nil {
			return &healthLiveOutput{Body: h.HealthLive()}, nil
		}
		return &healthLiveOutput{
			Body: HealthLiveResponse{
				Status: HealthStatusHealthy,
			},
		}, nil
	})

	huma.Register(hapi, huma.Operation{
		OperationID: "getHealthReady",
		Method:      http.MethodGet,
		Path:        PathHealthReady,
		Summary:     "Report operational readiness",
		Description: "Check determining whether the instance is capable of serving work.",
	}, func(_ context.Context, _ *struct{}) (*healthReadyOutput, error) {
		if h.HealthReady != nil {
			return &healthReadyOutput{Body: h.HealthReady()}, nil
		}
		return &healthReadyOutput{
			Body: HealthReadyResponse{
				Status: HealthStatusHealthy,
			},
		}, nil
	})

	huma.Register(hapi, huma.Operation{
		OperationID: "getHealthHA",
		Method:      http.MethodGet,
		Path:        PathHealthHA,
		Summary:     "Report high-availability and ownership diagnostics",
		Description: "Redundancy, lease, and primary ownership diagnostics.",
	}, func(_ context.Context, _ *struct{}) (*healthHAOutput, error) {
		if h.HealthHA != nil {
			return &healthHAOutput{Body: h.HealthHA()}, nil
		}
		inst := Instance{Role: InstanceRolePrimary, State: InstanceStateActive}
		if h.Instance != nil {
			inst = h.Instance()
		}
		role := inst.Role
		if role == "" {
			role = InstanceRolePrimary
		}
		state := inst.State
		if state == "" {
			state = InstanceStateActive
		}
		leaseState := LeaseStateOwned
		if state == InstanceStatePassive {
			leaseState = LeaseStateUnowned
		}
		return &healthHAOutput{
			Body: HealthHAResponse{
				Role:         role,
				RuntimeState: state,
				LeaseState:   leaseState,
			},
		}, nil
	})

	huma.Register(hapi, huma.Operation{
		OperationID: "getHealthServices",
		Method:      http.MethodGet,
		Path:        PathHealthServices,
		Summary:     "Report this instance's view of the site's deployed services",
		Description: "Every service the site deploys, what each observer last found, and whether those findings are current. " +
			"Answered by every instance in every state, from memory. " +
			"Unhealthy, Degraded, and Unknown services are data rather than a failure to answer, so this returns 200 for every valid view.",
	}, func(_ context.Context, _ *struct{}) (*healthServicesOutput, error) {
		if h.HealthServices != nil {
			return &healthServicesOutput{Body: h.HealthServices()}, nil
		}
		// A build with no service monitoring behind it answers an empty view
		// rather than an error: the question was answerable and the answer is that
		// this instance knows of no service. The slices are allocated so the
		// document carries empty arrays rather than nulls.
		return &healthServicesOutput{
			Body: ServiceHealthResponse{
				Services: []ServiceHealthUnit{},
				Distribution: ServiceHealthDistribution{
					State:    DistributionLocal,
					Rejected: []ServiceHealthCount{},
					Dropped:  []ServiceHealthCount{},
				},
			},
		}, nil
	})
}

// Register attaches every platform operation to hapi. It is the single source
// the runtime serves and the OpenAPI specification is generated from.
func Register(hapi huma.API, h Handlers) {
	RegisterInstance(hapi, h.Instance)
	RegisterHealth(hapi, h)
}

// OpenAPIYAML returns the platform's OpenAPI 3.0.3 document as YAML. It builds
// the API with zero Handlers, since generation never calls the handlers, and
// downgrades from huma's native 3.1 for tools such as Kiota.
func OpenAPIYAML() ([]byte, error) {
	mux := http.NewServeMux()
	cfg := Config()
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	hapi := humago.New(mux, cfg)
	Register(hapi, Handlers{})
	return hapi.OpenAPI().DowngradeYAML()
}
