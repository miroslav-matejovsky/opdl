package api

import (
	"context"
	"errors"
	"net/http"

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
// PathInstance and refuses the domain paths. Naming them once is what keeps that
// refusal list from drifting away from the operations registered below.
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
	// PathRegistrations lists and creates registration proposals.
	PathRegistrations = "/registrations"
	// PathRegistrationConflicts lists projected key conflicts.
	PathRegistrationConflicts = "/registrations/conflicts"
	// PathRegistrationByID reads one proposal's status.
	PathRegistrationByID = "/registrations/{proposal_id}"
)

// DomainPaths are the operations that need this node's authoritative projection,
// and which therefore only an Active instance can answer. Each entry is a Go 1.22
// ServeMux pattern: method and path.
//
// A Passive instance registers these to refuse them. It refuses rather than
// omitting them so that a caller that reached the wrong instance is told so, and
// told where to go, instead of getting the 404 an unrouted path would produce.
var DomainPaths = []string{
	http.MethodPost + " " + PathRegistrations,
	http.MethodGet + " " + PathRegistrations,
	http.MethodGet + " " + PathRegistrationConflicts,
	http.MethodGet + " " + PathRegistrationByID,
}

// ErrJournalUnavailable reports that a valid command could not be durably
// appended to the site journal. It maps to HTTP 503. It lives in the contract
// package because the status mapping is part of the API; the registration
// package returns it, and this package turns it into the 503 response.
var ErrJournalUnavailable = errors.New("api: site journal unavailable")

// Handlers is what the operations need to serve requests. The runtime fills it
// from the registration services. Spec generation leaves it zero: the handler
// closures are never called during generation, only their input and output types
// are read to build the OpenAPI document.
type Handlers struct {
	// Instance reports what this instance is and what it is doing. It is read on
	// every request rather than captured once, because State changes when Primary
	// Ownership moves.
	Instance func() Instance
	// Health reports primary operational health information.
	Health func() HealthResponse
	// HealthLive reports process liveness information.
	HealthLive func() HealthLiveResponse
	// HealthReady reports operational readiness information.
	HealthReady func() HealthReadyResponse
	// HealthHA reports high-availability and ownership diagnostics.
	HealthHA func() HealthHAResponse
	// Create validates and durably records a registration proposal. It returns
	// ErrJournalUnavailable when nothing was recorded and the client may retry.
	Create func(ctx context.Context, req RegistrationRequest) (ProposalAccepted, error)
	// List returns every proposal in journal order.
	List func() []Registration
	// Get returns one proposal by its canonical proposal ID.
	Get func(proposalID string) (Registration, bool)
	// Conflicts returns every projected key conflict in deterministic key order.
	Conflicts func() ([]RegistrationConflict, error)
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

type registerUnitInput struct {
	Body RegistrationRequest
}

type proposalAcceptedOutput struct {
	Body ProposalAccepted
}

type getStatusInput struct {
	ProposalID string `path:"proposal_id" doc:"Opaque proposal identifier returned by registerUnit." example:"c1a2b3"`
}

type registrationOutput struct {
	Body Registration
}

type registrationListOutput struct {
	Body []Registration
}

type conflictListOutput struct {
	Body []RegistrationConflict
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

// RegisterHealth attaches the health operations to hapi.
func RegisterHealth(hapi huma.API, h Handlers) {
	huma.Register(hapi, huma.Operation{
		OperationID: "getHealth",
		Method:      http.MethodGet,
		Path:        PathHealth,
		Summary:     "Report primary operational health",
		Description: "Overall health assessment, operational status, dependency status, readiness, and basic redundancy visibility.",
	}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		if h.Health != nil {
			return &healthOutput{Body: h.Health()}, nil
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
					"configuration":    HealthStatusHealthy,
					"internalServices": HealthStatusHealthy,
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
		exp := "2026-07-24T10:15:00Z"
		return &healthHAOutput{
			Body: HealthHAResponse{
				Role:                role,
				RuntimeState:        state,
				LeaseState:          leaseState,
				LeaseExpirationUTC:  &exp,
				OwnershipGeneration: 1,
			},
		}, nil
	})
}

// Register attaches every platform operation to hapi. It is the single source
// the runtime serves and the OpenAPI specification is generated from.
func Register(hapi huma.API, h Handlers) {
	RegisterInstance(hapi, h.Instance)
	RegisterHealth(hapi, h)

	huma.Register(hapi, huma.Operation{
		OperationID:   "registerUnit",
		Method:        http.MethodPost,
		Path:          PathRegistrations,
		Summary:       "Propose a unit registration",
		DefaultStatus: http.StatusAccepted,
		Errors:        []int{http.StatusBadRequest, http.StatusNotImplemented, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *registerUnitInput) (*proposalAcceptedOutput, error) {
		accepted, err := h.Create(ctx, in.Body)
		if err != nil {
			if errors.Is(err, ErrNotImplemented) {
				return nil, huma.Error501NotImplemented("not_implemented")
			}
			// A journal that will not take the proposal is the one failure the
			// client can act on: nothing was recorded, so retrying is safe.
			// Everything else the command service refuses is the request's own
			// fault and will fail again unchanged.
			if errors.Is(err, ErrJournalUnavailable) {
				return nil, huma.Error503ServiceUnavailable("journal_unavailable")
			}
			return nil, huma.Error400BadRequest("invalid_request")
		}
		return &proposalAcceptedOutput{Body: accepted}, nil
	})

	huma.Register(hapi, huma.Operation{
		OperationID: "listRegistrations",
		Method:      http.MethodGet,
		Path:        PathRegistrations,
		Summary:     "List registration proposals",
	}, func(ctx context.Context, _ *struct{}) (*registrationListOutput, error) {
		return &registrationListOutput{Body: h.List()}, nil
	})

	huma.Register(hapi, huma.Operation{
		OperationID: "listRegistrationConflicts",
		Method:      http.MethodGet,
		Path:        PathRegistrationConflicts,
		Summary:     "List resolved registration conflicts",
		Errors:      []int{http.StatusInternalServerError},
	}, func(ctx context.Context, _ *struct{}) (*conflictListOutput, error) {
		conflicts, err := h.Conflicts()
		if err != nil {
			return nil, huma.Error500InternalServerError("internal_error")
		}
		return &conflictListOutput{Body: conflicts}, nil
	})

	huma.Register(hapi, huma.Operation{
		OperationID: "getRegistrationStatus",
		Method:      http.MethodGet,
		Path:        PathRegistrationByID,
		Summary:     "Get a registration proposal's status",
		Errors:      []int{http.StatusNotFound},
	}, func(ctx context.Context, in *getStatusInput) (*registrationOutput, error) {
		view, found := h.Get(in.ProposalID)
		if !found {
			return nil, huma.Error404NotFound("registration_not_found")
		}
		return &registrationOutput{Body: view}, nil
	})
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
