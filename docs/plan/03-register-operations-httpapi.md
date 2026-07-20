# Stage 3: register operations in httpapi

## Goal

Replace the `net/http` `ServeMux` handler in `platform/internal/httpapi` with
huma operations. Expose one `Register(api, commands, queries)` function that both
the runtime (stage 5) and the spec generator (stage 4) call, so the served
contract and the generated spec have a single source.

## Current handler (what this replaces)

`platform/internal/httpapi/httpapi.go` today:

- `NewHandler(commands, queries) http.Handler` builds a `ServeMux`.
- Manual route ordering: `/registrations/conflicts` is registered before
  `/registrations/` so a conflict query is not mistaken for a status lookup.
- Manual JSON decode with `DisallowUnknownFields` and trailing-document rejection.
- Manual content-type check, method-not-allowed handling, and `{code}` error
  writing.
- `parseStatusPath` extracts the proposal ID from the path.

huma subsumes all of this: routing (Go 1.22 `ServeMux` picks the most specific
pattern, so the conflicts/status ordering hack is gone), body decoding and
validation, method handling, and content negotiation.

## Changes

### 1. Input and output structs

Add to `httpapi` (these wrappers belong to the consumer, not the contract, so
they stay out of `platform/api`):

```go
type registerUnitInput struct {
	Body api.RegistrationRequest
}
type proposalAcceptedOutput struct {
	Body api.ProposalAccepted
}

type getStatusInput struct {
	ProposalID string `path:"proposal_id"`
}
type registrationOutput struct {
	Body api.Registration
}

type registrationListOutput struct {
	Body []api.Registration
}
type conflictListOutput struct {
	Body []api.RegistrationConflict
}
```

### 2. The Register function

```go
func Register(hapi huma.API, commands *registration.CommandService, queries *registration.QueryService) {
	huma.Register(hapi, huma.Operation{
		OperationID:   "registerUnit",
		Method:        http.MethodPost,
		Path:          "/registrations",
		Summary:       "Propose a unit registration",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *registerUnitInput) (*proposalAcceptedOutput, error) {
		receipt, err := commands.Create(ctx, in.Body)
		if err != nil {
			if errors.Is(err, registration.ErrJournalUnavailable) {
				return nil, huma.Error503ServiceUnavailable("journal_unavailable")
			}
			return nil, huma.Error400BadRequest("invalid_request")
		}
		return &proposalAcceptedOutput{Body: api.ProposalAccepted{
			ProposalID: receipt.ProposalID,
			Sequence:   receipt.Sequence,
		}}, nil
	})

	huma.Register(hapi, huma.Operation{
		OperationID: "listRegistrations",
		Method:      http.MethodGet,
		Path:        "/registrations",
		Summary:     "List registration proposals",
	}, func(ctx context.Context, _ *struct{}) (*registrationListOutput, error) {
		return &registrationListOutput{Body: queries.List()}, nil
	})

	huma.Register(hapi, huma.Operation{
		OperationID: "listRegistrationConflicts",
		Method:      http.MethodGet,
		Path:        "/registrations/conflicts",
		Summary:     "List resolved registration conflicts",
	}, func(ctx context.Context, _ *struct{}) (*conflictListOutput, error) {
		conflicts, err := queries.Conflicts()
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
	}, func(ctx context.Context, in *getStatusInput) (*registrationOutput, error) {
		view, found := queries.Get(in.ProposalID)
		if !found {
			return nil, huma.Error404NotFound("registration_not_found")
		}
		return &registrationOutput{Body: view}, nil
	})
}
```

Note the empty-list responses (`List`, `Conflicts`) return an empty slice, same
as today.

### 3. Config helper

Both stage 4 and stage 5 need the same info metadata. Add one helper:

```go
func Config() huma.Config {
	cfg := huma.DefaultConfig("OPDL Platform API", "0.1.0")
	cfg.OpenAPI.Info.Description = "HTTP API served by the OPDL platform runtime."
	return cfg
}
```

(Confirm `cfg.OpenAPI.Info.Description` is the correct field path in v2.39.0.)

### 4. Remove the old handler

Delete `NewHandler`, `handleRegistrations`, `handleRegistrationConflicts`,
`handleRegistrationStatus`, `parseStatusPath`, `decodeJSON`, `isJSON`,
`methodNotAllowed`, `writeError`, `writeJSON`, and the `errorCode*` constants.
The runtime call site (`internal/app/runtime.go:119`) changes in stage 5; expect
`platform` not to compile between stage 3 and stage 5 unless you land stage 5
together. See the note below.

Update `platform/internal/httpapi/doc.go` to describe the huma-based handler.

## Decision: error model

huma's default error is RFC 9457 problem+json:
`{"status":400,"title":"Bad Request","detail":"invalid_request"}`. The stable
machine codes (`invalid_request`, `registration_not_found`, `journal_unavailable`,
`internal_error`) are passed as `detail` above.

- **Recommended (POC):** adopt the RFC 9457 default. Less code, idiomatic huma,
  richer errors including field-level validation from huma itself. The SDK error
  type changes shape.
- **Alternative:** keep the `{"code": "..."}` shape by overriding `huma.NewError`
  in one place (a package `init` or the runtime `main`) to return a custom
  `huma.StatusError` whose JSON body is `api.Error`. Keeps clients unchanged but
  discards huma's validation-error detail.

Whichever is chosen, keep the `platform/api.Error` type only if the alternative
is chosen; otherwise it is deleted in stage 6.

## Sequencing note

Stages 3, 4, and 5 are tightly coupled: stage 3 introduces `Register`, stage 4
consumes it for generation, stage 5 consumes it for serving and fixes the
runtime call site. Land them as three commits on one branch, or fold 3+5 into one
commit if you prefer the module to compile at every commit. The files are split
here for reviewability, not to force three compiling checkpoints.

## Verify

- `go build ./...` in `platform` once stage 5 lands.
- New table-driven handler tests in `httpapi_test.go` using
  `humatest.New(t, ...)` (huma's test adapter) to drive each operation and assert
  status codes and bodies. This replaces the current `ServeMux`-based tests.

## Exit criteria

One `Register` function serves every current operation through huma, with routing
and decoding delegated to huma, and the error model chosen and applied.
