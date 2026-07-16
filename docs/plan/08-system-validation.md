# Stage 8: System validation

Estimate: 8 person-days.

Complexity: high.

The additional three days cover file durability, direct synchronization, crash
windows, and rolling persistent-data checks.

## Objective

Prove the complete active-active behavior under normal traffic, failures, and a
rolling replacement. Finish all architecture, operator, and SDK documentation.

## Scenario matrix

Add deterministic black-box scenarios for at least these cases:

| Topology or event | Required observation |
| --- | --- |
| One default machine | Primary and secondary start, directly synchronize their stores, and both serve. |
| One primary-only machine | Only primary is expected and all operations work. |
| Two default machines | Four fabric members, two registration machine voters, and identical site views. |
| Mixed two-machine site | Three fabric members and two registration machine voters. |
| Primary hard failure | Existing SDK client continues through secondary and acknowledged state remains. |
| Secondary hard failure | Existing SDK client continues through primary and acknowledged state remains. |
| Process recovery | Restarted instance rejoins without duplicate domain state. |
| Local sync without Olric | Facts written by either instance copy to the sibling store while Olric is unavailable. |
| Pre-sync writer loss | A writer is hard-killed immediately after acknowledgment; the sibling reads the writer's file and completes the copy. |
| Interrupted file write | No partial fact is exposed and the last durable state remains recoverable. |
| Local fact mismatch | The same fact ID with different content fails visibly and does not select a winner by read order. |
| Concurrent active traffic | Both endpoints receive first attempts and results converge. |
| Rolling N to N+1 | Traffic and registration progress continue through sequential replacement. |
| Both local processes unavailable | SDK returns a bounded aggregate failure with both attempts. |
| Whole expected machine unavailable | Other machines serve, but registration waits for that machine under the existing barrier. |
| Socket override | One instance moves all approved sockets without changing identity. |
| Invalid collision | Startup fails before opening partial runtime resources. |

Use handshakes and observable readiness, not sleeps, for cross-process ordering.
Keep failures bounded with useful diagnostics containing machine, instance,
endpoint, process output, and event paths.

## Validation work

1. Update the scenario harness so a machine owns primary and optional secondary
   process handles. Support independent start, readiness wait, graceful stop,
   hard kill, restart, captured output, own-store inspection, and direct
   synchronization control per instance.
2. Update Go black-box API structs and generated .NET E2E tests for the final
   machine progress contract.
3. Drive failure scenarios through the hand-written redundant SDK path. Raw URL
   calls may be used only for platform-specific diagnostics and focused API
   assertions.
4. Verify event files contain the correct machine and instance node. Assert
   domain events semantically, allowing only the documented duplicates caused
   by membership change.
5. Verify active-active operation, not merely standby availability. Record an
   observable request or reconciliation action from both instances during the
   healthy phase.
6. Add conformance fixtures for redundant and primary-only descriptor JSON,
   generated API artifacts, supported N/N+1 registration records, and supported
   durable store formats.
7. Run targeted race tests for Go shared in-memory fixtures and .NET concurrent
   endpoint selection where supported by the toolchain.
8. Run repeated failure scenarios enough times to expose membership and socket
   races, file flush windows, and concurrent sibling scans. Do not introduce
   timing-based acceptance assertions.
9. Run `task all`, inspect generated diffs, and confirm the builder restores the
   neutral embedded descriptor after packaging tests.
10. Run the local storage and synchronizer scenarios with Olric disabled or
    isolated. Assert convergence by durable fact IDs in both stores. Fabric
    membership, events, or replica counts are not evidence of local sync.
11. Hard-kill a writer at controlled boundaries before write, after durable
    write but before copy, during copy, and after both stores converge. Use
    handshakes or fault hooks rather than sleeps. Assert only acknowledged facts
    are required to survive.
12. Exercise the rolling procedure with persistent data paths outside both N
    and N+1 installation trees. Verify upgrade, failed upgrade rollback, and
    later cleanup preserve both stores.

## Documentation completion

Update these documents to describe implemented behavior rather than this plan:

- root `README.md` repository and run overview;
- `docs/01-architecture.md` descriptor, membership, lifecycle, redundancy,
  durability, and upgrade guarantees;
- `docs/02-registration.md` machine voting and same-machine endpoint behavior;
- `docs/README.md` links and reading order;
- `platform/README.md` instance selection and process ownership;
- `sdk-dotnet/README.md` redundant client construction and failure semantics;
- `conformance-tests/README.md` new descriptor and compatibility fixtures;
- `platform/config.toml` complete primary/secondary override example;
- affected Go `doc.go` files and public Go/.NET API documentation.

Remove every stale statement that the platform has no redundancy or one fabric
member per machine. Preserve documented limitations for whole-machine failure,
full-site shutdown, disk or store corruption, Olric membership changes, and
primary-only deployments. State that same-machine store synchronization is an
OPDL-owned mechanism independent of Olric.

## Reliability acceptance criteria

- With either platform process absent, the other serves every public operation
  and registrations can reach acceptance.
- The SDK routes normal traffic to both healthy instances and fails over without
  consumer endpoint selection.
- No test or API confuses platform primary/secondary with service Master/Slave.
- One-process loss does not lose state acknowledged before the failure, within
  the documented guarantee.
- Primary and secondary stores converge with Olric unavailable, and a surviving
  process can recover a fact from the stopped writer's readable store before it
  was copied.
- A rolling replacement keeps at least one ready compatible process and does
  not produce consumer-visible transient failures in the test workload.
- Descriptor, config, API, generated SDK, package metadata, and documentation
  agree on names and defaults.
- Secondary-disabled machines behave correctly and explicitly lack local
  zero-downtime upgrade guarantees.
- Whole-machine loss, disk loss, store corruption, and loss of both store paths
  remain outside the acknowledged-state guarantee.
- `task all` passes from a clean working tree except for the intended changes.

## Exit criteria

The feature is complete only when every applicable scenario and reliability
criterion above passes. Any remaining production blocker must be written to the
root `.todo` with an owner and impact before calling the implementation done.
