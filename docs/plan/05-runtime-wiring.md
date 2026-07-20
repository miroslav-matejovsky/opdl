# Stage 5: runtime wiring

## Goal

Serve the huma API from the platform runtime, updating the `httpapi.NewHandler`
call for its new signature, and decide whether to expose `/openapi` and `/docs`
on the running server.

## Current call site (what this replaces)

`platform/internal/app/runtime.go`, in `runActive`, around line 117:

```go
srv := &http.Server{
	Addr:              addr,
	Handler:           httpapi.NewHandler(site.commands, site.queries),
	ReadHeaderTimeout: cfg.ReadHeaderTimeout(),
}
```

`serve`/`serveListener` in `app.go` own the listener and graceful shutdown and do
not change. Only the `Handler` construction changes.

## Changes

`httpapi.NewHandler` now takes an `exposeSpec` bool (stage 3) and returns the huma
`ServeMux` built in `platform/api`. The runtime keeps calling `httpapi`, so
`app` does not import huma or `api` directly:

```go
srv := &http.Server{
	Addr:              addr,
	Handler:           httpapi.NewHandler(site.commands, site.queries, false),
	ReadHeaderTimeout: cfg.ReadHeaderTimeout(),
}
```

That is the whole runtime change. All the huma construction lives in
`api.NewServeMux` (called by `httpapi.NewHandler`), so arch-lint stays as in
stage 1: `api canUse: [huma]`, `httpapi mayDependOn: [api, registration]`, and
`app mayDependOn: [..., httpapi]` unchanged.

## Decision: expose the spec over HTTP

The user marked HTTP exposure optional. The authoritative artifact is
`api-specifications/openapi.yaml` in git, produced in stage 4.

- **Recommended:** pass `false`. The runtime serves only the four registration
  operations, the same surface as today. huma's `/openapi`, `/openapi.json`,
  `/docs`, and `/schemas` routes are disabled by the empty paths in
  `api.NewServeMux`. Turning it on later is a one-line change.
- Alternative: pass `true` to serve huma's built-in `/openapi.yaml`,
  `/openapi.json`, and the `/docs` UI, useful for local exploration. It adds
  routes the platform did not serve before; if enabled, consider gating it so it
  is off in production.

If exposure should be configurable rather than hard-coded, thread a bool from
`internal/config` into `NewHandler`. That is a small addition to the config
struct and its TOML; defer it unless wanted for the POC.

## Verify

- `task run` starts the runtime; `POST /registrations`, `GET /registrations`,
  `GET /registrations/{id}`, and `GET /registrations/conflicts` behave as before
  (use the `/run` skill or curl).
- If exposure is on, `GET /openapi.yaml` returns the spec and `/docs` renders.
- Scenarios that exercise the HTTP API still pass. Per the memory note, scenarios
  build the binary at run time; after editing `platform/**` re-run them with
  `-count=1`.

## Exit criteria

The runtime serves the huma API through the updated `httpapi.NewHandler`, and
spec exposure is set to the chosen default.
