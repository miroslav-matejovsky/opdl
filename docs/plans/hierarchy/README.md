# Plan: finish site events and registration

Status: draft for review. Updated 2026-07-28 from branch `mirmat-wip` at
`4aa26aa`, compared with `main`.

## Implemented baseline

The original hierarchy plan's steps 01 through 05 are implemented and are no
longer part of this plan:

- the shared event contract lives in `platform/internal/events`;
- dependency direction is enforced by `depguard` and `go-arch-lint`;
- envelopes carry `instance`, `machine`, or `site` scope;
- every process writes its instance event log;
- every machine has an append-only shared event store for machine-scoped facts;
- the runtime publisher fans one stamped envelope to the instance log and the
  scope-filtering machine backend; and
- `internal/site/eventfabric` defines the ordered delivery and durable-consumer
  contract required by registration.

The instance and machine stores have no read API. This is deliberate. No
platform behavior consumes them.

## Remaining gap

Site distribution has no implementation. `app.hasEventStorage` returns `false`,
`app.open`, `registration.CommandService.Create`, and
`registration.NewHandler` are stubs, and `app.topology` knows only the local
machine. Registration therefore returns `503` on every deployment.

Registration's projection already depends on one total site order. The first
proposal for a key by `eventfabric.Delivery.Sequence` wins. The next step must
either preserve that order or explicitly change the domain rule before transport
work starts.

## Remaining steps

| Step | Title | Complexity | Effort | Depends on |
| --- | --- | --- | --- | --- |
| [06](06-site-distribution-adr.md) | Decide site distribution and its event-backend contract | Medium | 2-3 d | implemented baseline |
| [07](07-single-machine-site.md) | Implement the single-machine site slice and registration | High | 5-8 d | 06 |
| [08](08-multi-machine-distribution.md) | Extend the chosen distribution to multiple machines | High | 8-12 d | 06, 07 |
| [09](09-service-activation-outlook.md) | Record service-activation constraints | Low | 1-2 d | 06 |

```text
06 -> 07 -> 08
 |
 +------> 09
```

## Constraints

- Local Primary Ownership remains independent of site distribution.
- Scope routes envelopes. Package location does not.
- Every event reaches the stating instance's event log.
- Machine and site adapters filter the common publisher flow by envelope scope.
- Registration correctness lives in deterministic domain identities and the
  selected convergence rule, not transport deduplication.
- New behavior requires unit tests and Windows black-box scenarios.
