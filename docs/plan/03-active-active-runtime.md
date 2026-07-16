# Stage 3: Active-active runtime

Estimate: 5 person-days.

## Objective

Run primary and secondary as separate, simultaneously active platform processes
from one machine package. Give each process isolated sockets and observability
while retaining the same compiled machine identity.

## Implementation steps

1. Add the approved explicit instance selector to `platform/cmd` and
   `internal/app`. Validate it against the embedded descriptor before opening
   the event sink, fabric, or API listener.
2. Resolve one effective instance configuration from descriptor defaults plus
   the matching TOML override. Pass that value through runtime composition
   rather than repeatedly selecting by string in subsystems.
3. Refactor runtime composition only as far as needed to make instance identity
   explicit. Each process still owns one recorder, one fabric member, one
   registration service and reconciler, and one HTTP server.
4. Start both processes independently. Do not have primary spawn secondary or
   secondary monitor primary. Process supervision belongs to Stage 6.
5. Include machine and platform instance in startup summaries, errors, and
   lifecycle logs. Avoid the words leader, follower, master, slave, active
   role, and passive role for platform instances.
6. Add platform instance to `events.Node` and JSONL filenames so concurrent
   recorders never append through separate file handles to the same path.
7. Preserve strict startup order per process: recorder, selected fabric member,
   registration and initial reconciliation, then selected HTTP listener.
8. Preserve reverse shutdown order and graceful HTTP drain per process. Stopping
   one process must not signal or close resources owned by its sibling.
9. Decide whether an explicit readiness endpoint is needed by the Stage 0
   supervisor. If needed, expose readiness only after initial reconciliation and
   stop reporting ready before HTTP drain. If TCP/API readiness is sufficient,
   document that contract instead of adding an endpoint.
10. Update `task run` and scenario helpers to pass an explicit instance. A helper
    should model a machine with one or two process handles, not call one process
    a machine.

## Tests

- Missing, unknown, and disabled-secondary selectors fail before any listener
  or event file opens.
- Primary and secondary start concurrently from the same embedded descriptor.
- Both APIs serve the same operations on their distinct addresses.
- Both reconcilers run and both fabric lifecycle logs identify the correct
  instance.
- Event files are distinct and contain only the expected event node.
- Failure to bind secondary does not stop an already running primary.
- Gracefully stopping primary drains its requests while secondary continues to
  serve, and the reverse case also works.
- A primary-only machine starts normally and rejects a secondary launch.
- Cancellation and partial-start cleanup remain covered for each selected
  instance.

## Operational behavior

Both instances perform useful work during normal operation. This stage does not
implement traffic balancing inside the platform. Clients may call either API,
and the SDK in Stage 5 will distribute traffic.

The instance labels establish stable endpoint and process identities. They do
not grant primary extra authority. Any future behavior that prefers primary
must be proposed separately and must not silently turn secondary into standby.

## Exit criteria

- Two independent processes run concurrently from one machine package.
- Either process can start, serve, and stop without controlling the other.
- Runtime resources and event output are instance-safe.
- Primary-only configuration works without placeholder secondary resources.
- `task all` passes.
