# OPDL documentation

Read the documentation in this order:

1. [Architecture](01-architecture.md) describes the build pipeline, runtime
   boundaries, fabric, startup, shutdown, and event model.
2. [Registration](02-registration.md) describes the site-wide registration
   protocol, conflict convergence, and HTTP behavior.

The numbered prefixes are the recommended reading order. They do not indicate
implementation stages.

Additional material:

- [Backlog](backlog/README.md) contains deferred, actionable work.
- [Bugs](bugs/README.md) contains known issues.
- [Event migration findings](plan/findings/README.md) classifies resolved
  decisions, accepted risks, deployment requirements, and open work.
- Package-level details live next to Go code in `doc.go` files, or in `README.md` for top-level directories like `platform`.
