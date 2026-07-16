# OPDL documentation

OPDL is designed for on-premises, highly regulated production environments.
Deployment facts belong in the embedded deployment descriptor so the running
system is explicit, auditable, and maintainable. Runtime configuration exists
only as a controlled fallback for exceptional production fixes and must not
become a competing source of topology or identity.

Read the documentation in this order:

1. [Architecture](01-architecture.md) describes the build pipeline, runtime
   boundaries, fabric, startup, shutdown, and event model.
2. [Registration](02-registration.md) describes the site-wide registration
   protocol, conflict convergence, and HTTP behavior.

The numbered prefixes are the recommended reading order. They do not indicate
implementation stages.

Additional material:

- [Platform redundancy plan](plan/README.md) tracks the staged implementation
  and the decisions required before later stages begin.
- [Backlog](backlog/README.md) contains deferred, actionable work.
- [Bugs](bugs/README.md) contains known issues.
- Package-level details live next to Go code in `doc.go` files, or in `README.md` for top-level directories like `platform`.
