# OPDL documentation

Read the documentation in this order:

1. [Architecture](01-architecture.md) describes the build pipeline, runtime
   boundaries, startup, shutdown, and local redundancy.
2. [Events](02-events.md) describes the event contract, scope, storage, and the
   current site-distribution gap.
3. [Registration](03-registration.md) describes the site-wide registration
   protocol, conflict convergence, and HTTP behavior.
4. [Logging](04-logging.md) describes the structured application log each
   instance writes, and how it differs from an event.

The numbered prefixes are the recommended reading order. They do not indicate
implementation stages.

Additional material:

- [Drafts](drafts) contains work-in-progress models and requirements that are
  not yet formal specifications.
- [Backlog](backlog/README.md) contains deferred, actionable work.
- [Bugs](bugs/README.md) contains known issues.
- Package-level details live next to Go code in `doc.go` files, or in `README.md` for top-level directories like `platform`.
