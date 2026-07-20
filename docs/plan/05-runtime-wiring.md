# Stage 5: runtime wiring

## Goal

Serve the huma API from the platform runtime, replacing the old
`httpapi.NewHandler` call, and decide whether to expose `/openapi.yaml` and
`/docs` on the running server.

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

Build a `http.ServeMux`, bind a huma API to it, register the operations, and hand
the mux to the server:

```go
mux := http.NewServeMux()
hcfg := httpapi.Config()
// Optional spec exposure, see the decision below. Off by default:
hcfg.OpenAPIPath = ""
hcfg.DocsPath = ""
hcfg.SchemasPath = ""
hapi := humago.New(mux, hcfg)
httpapi.Register(hapi, site.commands, site.queries)

srv := &http.Server{
	Addr:              addr,
	Handler:           mux,
	ReadHeaderTimeout: cfg.ReadHeaderTimeout(),
}
```

Keep this construction in `app`, or move it behind a small
`httpapi.NewServeMux(commands, queries, exposeSpec bool) http.Handler` so `app`
does not import `humago`. Prefer the helper: it keeps the huma adapter import
inside `httpapi` and leaves `runtime.go` calling one platform function, close to
how `NewHandler` is called today.

```go
// platform/internal/httpapi/httpapi.go
func NewServeMux(commands *registration.CommandService, queries *registration.QueryService, exposeSpec bool) http.Handler {
	mux := http.NewServeMux()
	cfg := Config()
	if !exposeSpec {
		cfg.OpenAPIPath = ""
		cfg.DocsPath = ""
		cfg.SchemasPath = ""
	}
	Register(humago.New(mux, cfg), commands, queries)
	return mux
}
```

Then `runtime.go` becomes:

```go
Handler: httpapi.NewServeMux(site.commands, site.queries, false),
```

If a helper is used, arch-lint stays as in stage 1 (`httpapi canUse: [huma]`);
`app` still only `mayDependOn: [httpapi]` and never imports huma.

## Decision: expose the spec over HTTP

The user marked HTTP exposure optional. The authoritative artifact is
`api-specifications/openapi.yaml` in git, produced in stage 4.

- **Recommended:** keep exposure off (`exposeSpec=false`). The runtime serves
  only the four registration operations, identical surface to today. Turning it
  on later is a one-line flag.
- Alternative: pass `true` to serve huma's built-in `/openapi.yaml`,
  `/openapi.json`, and the `/docs` UI. Useful for local exploration; adds routes
  the platform did not serve before. If enabled, consider gating it behind config
  so it is off in production.

If exposure should be configurable rather than hard-coded, thread a bool from
`config` (`internal/config`) into `NewServeMux`. That is a small addition to the
config struct and its TOML; defer it unless wanted for the POC.

## Verify

- `task run` starts the runtime; `POST /registrations`, `GET /registrations`,
  `GET /registrations/{id}`, and `GET /registrations/conflicts` behave as before
  (use the `/run` skill or curl).
- If exposure is on, `GET /openapi.yaml` returns the spec and `/docs` renders.
- Scenarios that exercise the HTTP API still pass. Per the memory note, scenarios
  build the binary at run time; after editing `platform/**` re-run them with
  `-count=1`.

## Exit criteria

The runtime serves the huma API, the old `NewHandler` call site is gone, and spec
exposure is set to the chosen default.
