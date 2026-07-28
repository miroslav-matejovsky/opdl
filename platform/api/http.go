package api

import (
	"context"
	"fmt"
	"net/http"
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
// response from the live instance identity, the process start time, the current
// lease, and this instance's event fabric. They are read on every request rather
// than captured once, because Role, State, the lease, and the fabric all change
// while the process runs.
//
// leaseView may be nil, in which case /health/ha derives ownership from the
// runtime state alone. eventFabric may be nil, in which case /health reports no
// fabric check rather than claiming a healthy one. Both are nil for spec
// generation and the boundary tests, which serve the shape without a runtime
// behind it.
//
// A failing event fabric makes the instance Degraded, not Unhealthy. Unhealthy
// on this endpoint is the gate a Passive instance promotes through, and moving
// Primary Ownership would not fix a broken fabric: the other instance runs its
// own embedded broker and its own client, so it has nothing better to offer. The
// instance stays the machine's serving instance and says what is wrong with it.
func NewHealth(instance func() Instance, started time.Time, leaseView func() LeaseView, eventFabric func(context.Context) error) Handlers {
	return Handlers{
		Health: func(ctx context.Context) HealthResponse {
			inst := instance()
			checks := map[string]string{
				HealthCheckConfiguration:    HealthStatusHealthy,
				HealthCheckInternalServices: HealthStatusHealthy,
			}
			status := HealthStatusHealthy
			if eventFabric != nil {
				checks[HealthCheckEventFabric] = HealthStatusHealthy
				if err := eventFabric(ctx); err != nil {
					checks[HealthCheckEventFabric] = HealthStatusUnhealthy
					status = HealthStatusDegraded
				}
			}
			return HealthResponse{
				Status:       status,
				InstanceID:   inst.Role,
				Role:         inst.Role,
				RuntimeState: inst.State,
				Version:      apiVersion,
				Uptime:       humanizeUptime(time.Since(started)),
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
			inst := instance()
			// Without a lease view, fall back to the runtime state: an Active
			// instance owns, a Passive one does not.
			view := LeaseView{Owned: inst.State == InstanceStateActive}
			if leaseView != nil {
				view = leaseView()
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
	}
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
				Checks: map[string]string{
					HealthCheckConfiguration:    HealthStatusHealthy,
					HealthCheckInternalServices: HealthStatusHealthy,
				},
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
