# Stage 3: register operations

## Goal

Move the HTTP operation definitions into `platform/api` as huma operations, with
a single `Register` function that both the runtime and the spec generator use.
Reduce `platform/internal/httpapi` to a thin layer that wires the registration
services into `api.Handlers`.

## Where the code lives and why

`platform/api` owns the huma operations. It imports huma but must not import
`internal/registration` (registration already imports `api`, so importing it
back would cycle). The operations therefore depend on a small `Handlers` struct
of function fields, which the runtime fills from the services. This is the seam
that keeps `api` free of the domain packages while still being the single source
the spec is generated from.

## Current handler (what this replaces)

`platform/internal/httpapi/httpapi.go` today builds a `ServeMux` with manual
routing (including the `/registrations/conflicts` before `/registrations/`
ordering hack), manual JSON decode with `DisallowUnknownFields`, content-type
checks, method handling, and `{code}` error writing. huma subsumes all of it:
Go 1.22 `ServeMux` picks the most specific pattern, so the ordering hack is gone,
and huma owns decoding, validation, method handling, and content negotiation.

## Changes

### 1. `platform/api`: operations, handlers, config, export

Add a new file, for example `platform/api/http.go`:

```go
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// ErrJournalUnavailable reports that a valid command could not be durably
// appended to the site journal. It maps to HTTP 503. It lives here because the
// status mapping is part of the API contract; the registration package returns
// it (registration already imports this package).
var ErrJournalUnavailable = errors.New("api: site journal unavailable")

// Handlers is what the operations need to serve requests. The runtime fills it
// from the registration services. Spec generation leaves it zero: the handler
// closures are never called during generation, only their input/output types are
// read.
type Handlers struct {
	Create    func(ctx context.Context, req RegistrationRequest) (ProposalAccepted, error)
	List      func() []Registration
	Get       func(id string) (Registration, bool)
	Conflicts func() ([]RegistrationConflict, error)
}

const (
	apiTitle       = "OPDL Platform API"
	apiVersion     = "0.1.0"
	apiDescription = "HTTP API served by the OPDL platform runtime."
)

// Config returns the huma configuration for the platform API.
func Config() huma.Config {
	cfg := huma.DefaultConfig(apiTitle, apiVersion)
	cfg.OpenAPI.Info.Description = apiDescription
	return cfg
}

type registerUnitInput struct{ Body RegistrationRequest }
type proposalAcceptedOutput struct{ Body ProposalAccepted }
type getStatusInput struct {
	ProposalID string `path:"proposal_id"`
}
type registrationOutput struct{ Body Registration }
type registrationListOutput struct{ Body []Registration }
type conflictListOutput struct{ Body []RegistrationConflict }

// Register attaches every platform operation to hapi.
func Register(hapi huma.API, h Handlers) {
	huma.Register(hapi, huma.Operation{
		OperationID:   "registerUnit",
		Method:        http.MethodPost,
		Path:          "/registrations",
		Summary:       "Propose a unit registration",
		DefaultStatus: http.StatusAccepted,
		Errors:        []int{http.StatusBadRequest, http.StatusServiceUnavailable},
	}, func(ctx context.Context, in *registerUnitInput) (*proposalAcceptedOutput, error) {
		accepted, err := h.Create(ctx, in.Body)
		if err != nil {
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
		Path:        "/registrations",
		Summary:     "List registration proposals",
	}, func(ctx context.Context, _ *struct{}) (*registrationListOutput, error) {
		return &registrationListOutput{Body: h.List()}, nil
	})

	huma.Register(hapi, huma.Operation{
		OperationID: "listRegistrationConflicts",
		Method:      http.MethodGet,
		Path:        "/registrations/conflicts",
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
		Path:        "/registrations/{proposal_id}",
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

// NewServeMux builds the served http.Handler. When exposeSpec is true, huma's
// generated /openapi and /docs endpoints are served too.
func NewServeMux(h Handlers, exposeSpec bool) http.Handler {
	mux := http.NewServeMux()
	cfg := Config()
	if !exposeSpec {
		cfg.OpenAPIPath = ""
		cfg.DocsPath = ""
		cfg.SchemasPath = ""
	}
	Register(humago.New(mux, cfg), h)
	return mux
}

// OpenAPIYAML returns the platform's OpenAPI 3.0.3 document as YAML. It builds
// the API with zero Handlers, since generation never calls the handlers.
func OpenAPIYAML() ([]byte, error) {
	mux := http.NewServeMux()
	cfg := Config()
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	hapi := humago.New(mux, cfg)
	Register(hapi, Handlers{})
	return hapi.OpenAPI().DowngradeYAML()
}
```

The `Errors` lists are what put the 400/404/500/503 responses into the generated
spec (with the RFC 9457 error schema). They replace the explicit `Response`
entries in the old `Describe()`.

### 2. Move the sentinel out of `registration`

`registration/service.go` defines and wraps `ErrJournalUnavailable`. Delete that
`var` and use `api.ErrJournalUnavailable` in `CommandService.Create`'s wrap:

```go
return ProposalReceipt{}, fmt.Errorf("%w: publish proposal %s: %w", api.ErrJournalUnavailable, proposed.ProposalID, err)
```

Update any test that references `registration.ErrJournalUnavailable`.

### 3. `platform/internal/httpapi`: thin wiring

Replace the whole `ServeMux` handler with a `NewHandler` that builds
`api.Handlers` from the services:

```go
package httpapi

import (
	"context"
	"net/http"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

// NewHandler wires the registration services into the platform's huma API.
func NewHandler(commands *registration.CommandService, queries *registration.QueryService, exposeSpec bool) http.Handler {
	return api.NewServeMux(api.Handlers{
		Create: func(ctx context.Context, req api.RegistrationRequest) (api.ProposalAccepted, error) {
			receipt, err := commands.Create(ctx, req)
			if err != nil {
				return api.ProposalAccepted{}, err
			}
			return api.ProposalAccepted{ProposalID: receipt.ProposalID, Sequence: receipt.Sequence}, nil
		},
		List:      queries.List,
		Get:       queries.Get,
		Conflicts: queries.Conflicts,
	}, exposeSpec)
}
```

`httpapi` imports `api` and `registration`; it does not import huma. Delete the
old handler functions, decoders, and `errorCode*` constants. Update
`platform/internal/httpapi/doc.go`.

## Decision: error model

huma's default error is RFC 9457 problem+json:
`{"status":503,"title":"Service Unavailable","detail":"journal_unavailable"}`.
The stable machine codes are passed as `detail` in the sketch above.

- **Recommended (POC):** adopt the RFC 9457 default. Less code, idiomatic huma,
  and huma adds field-level validation errors for free. The SDK error type
  changes shape. Delete `platform/api.Error` in stage 6.
- **Alternative:** keep `{"code": "..."}` by overriding `huma.NewError` once (in
  a package `init` in `api`, or in the runtime `main`) to return a custom
  `huma.StatusError` whose body is `api.Error`. Keep `api.Error` if so.

## Sequencing note

Stages 3, 4, and 5 are coupled: stage 3 introduces the operations and
`OpenAPIYAML`/`NewServeMux`, stage 4 consumes `OpenAPIYAML` for generation, stage
5 fixes the runtime call site for the new `NewHandler` signature. Land them
together on one branch, or fold 3+5 into one commit if you want `platform` to
compile at every commit. The split here is for reviewability.

## Verify

- `go build ./...` in `platform` once stage 5 lands.
- New table-driven tests using `humatest.New(t, ...)` (huma's test adapter)
  driving each operation, asserting status codes and bodies. Put these in
  `platform/api` (they can build `Handlers` with in-memory funcs, no registration
  dependency) and/or in `httpapi` with the real services. This replaces the old
  `ServeMux`-based tests.

## Exit criteria

`platform/api` owns the operations, `Register`, `Config`, `NewServeMux`, and
`OpenAPIYAML`; `httpapi` is a thin wiring layer; the sentinel lives in `api`; and
the error model is chosen and applied.
