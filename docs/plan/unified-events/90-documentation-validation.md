# Stage 90: reconcile documentation and validate the refactor

Effort: S-M, about 0.5 to 1 day. Complexity: Low-Medium.

## Goal

Make the documented event architecture match the implemented canonical model and
complete repository validation.

## Documentation

Update:

- root architecture documentation for one event model and wrapper;
- `platform/README.md` for canonical local and journal event output;
- `internal/events/doc.go` for payload ownership, envelope fields, defaults, and
  factory behavior;
- `internal/operations/doc.go` so it describes a local envelope writer, not a
  second event concept;
- `internal/eventfabric/doc.go` so adapters receive completed envelopes;
- `internal/eventfabric/nats/doc.go` for typed local adapter events;
- owner package docs where their event catalogs changed.

Document these invariants explicitly:

- concrete event payloads live in owner `events.go` files;
- common metadata is stamped automatically;
- business code emits typed payloads only;
- transport order is delivery metadata;
- local operational output remains usable during NATS failure;
- storage and distribution do not define event shape.

Remove documentation for `Meta`, `Record`, `operations.Event`, string/map
emission, and adapter-side stamping. Do not document compatibility behavior for
the removed JSON formats.

## Final review

Review call sites for readability. A normal event emission should be one typed
payload construction and one publish or record call. Reject helpers that hide
domain data or require callers to supply technical metadata.

Confirm every event-owning package has one `events.go` with a complete catalog
comment. Confirm `internal/events` contains no concrete application event.

## Validation

Run focused tests while resolving final failures, then run:

```text
task all
```

The final gate must pass. Do not run black-box scenarios separately unless they
are added to the repository gate or explicitly requested.

## Acceptance criteria

- Architecture and package documentation describe one event model.
- All stale API and wrapper names are absent from code and docs.
- Event emission call sites contain no technical metadata plumbing.
- The full validation gate passes.
