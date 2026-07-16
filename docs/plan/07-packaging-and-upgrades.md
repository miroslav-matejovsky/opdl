# Stage 7: Packaging and upgrades

Estimate: 7 person-days.

Complexity: high.

The additional two days cover persistent store layout, permissions,
synchronization gates, backup-safe replacement, and rollback.

## Objective

Make machine packages self-describing for two process launches and two stable
instance-owned data paths. Define a repeatable rolling upgrade that keeps one
active compatible instance available and keeps both stores converged without
using Olric as the local synchronization path.

## Implementation steps

1. Extend package manifest metadata with the configured platform instances,
   their descriptor endpoints, and their launch selectors. A primary-only
   package lists exactly one launch.
2. Keep one machine-specific binary unless Stage 0 selects a different process
   model. Both launch entries reference it with different instance arguments.
3. Define a versioned installation layout that does not overwrite the running
   binary in place. Include checksums for all shipped configuration templates
   and launch metadata.
4. Define the supervisor contract: start, stop, restart, readiness wait,
   graceful timeout, environment/working directory, log capture, and rollback.
   Provide platform-neutral manifest data first. Add Windows Service or systemd
   integration only if Stage 0 assigns it to this repository.
5. Ensure primary and secondary never share a writable event file, PID file,
   temporary path, store path, or mutable working file. Each launch has write
   access to its own persistent store and read access to the sibling store.
   Shared read-only descriptor and binary content is allowed.
6. Document and automate the rolling sequence where possible:
   verify secondary ready and directly synchronized, drain and stop primary N,
   start primary N+1 against the preserved primary store, wait ready,
   compatible, and synchronized, drain and stop secondary N, start secondary
   N+1 against the preserved secondary store, wait ready and synchronized, then
   verify both.
7. Define rollback before the first replacement. If primary N+1 fails readiness
   or compatibility, stop it and restart primary N while secondary N stays
   active. Do not continue to secondary.
8. Add compatibility guards. A process must fail before serving if descriptor,
   fabric protocol, registration record, durable store, or sibling
   synchronization versions cannot coexist with the running sibling during the
   supported N/N+1 window.
9. Define primary-only upgrade behavior honestly. A machine with secondary
   disabled cannot have process-level zero downtime on that machine. Package
   output and documentation must state this consequence at build and operation
   time.
10. Add a builder plan/manifest warning for explicit secondary disablement so
    the resource choice is visible before deployment.
11. Put both store directories outside the versioned installation tree. Package
    install, rollback, and cleanup must never replace or delete them. Include
    their paths, owner instance, format version, and required access mode in
    launch metadata without checksumming mutable data. Provision both empty
    store locations before either process first starts.
12. Define the filesystem access contract. Application capabilities always
    enforce own-write/sibling-read. If the production supervisor uses separate
    service identities, document the matching ACLs. If both run as one OS user,
    state that OS permissions cannot enforce the per-process write restriction.
13. Add a pre-stop and post-start bounded synchronization check. Failure to
    converge blocks advancement to the next instance and triggers the existing
    rollback path. The check must call the direct local synchronizer, not infer
    convergence from Olric membership or replica counts.

## Rolling-upgrade guarantees

The target guarantee is:

- API calls through the redundant SDK continue while either process is stopped.
- New registrations can be created, queried, and accepted while one process is
  stopped, subject to other machines being available.
- Acknowledged state survives each single-process replacement.
- Primary and secondary store synchronization succeeds without Olric acting as
  the local replication channel.
- In-flight requests on the stopping process receive the configured graceful
  drain window.
- Mixed N/N+1 processes exchange fabric and registration data only within the
  declared compatibility window.

This plan does not guarantee availability when both same-machine processes are
stopped, the whole machine fails, or the external supervisor routes both down at
once.

## Tests

- Package manifest contains exactly the descriptor's launch entries and
  endpoints.
- Package manifest maps each launch to one writable own store and one optional
  read-only sibling store, with distinct persistent paths.
- Both entries start from an unpacked package with no generated local edits.
- A primary-only package contains no secondary launch entry and emits the
  approved warning.
- Rolling restart preserves SDK traffic and acknowledged registration state.
- Rolling restart preserves both stores, verifies direct synchronization before
  each stop, and proves convergence from durable store contents rather than
  Olric membership or replica observations.
- Failure of primary N+1 readiness triggers rollback without stopping secondary
  N.
- Unsupported mixed versions fail before public readiness.
- Checksums and release metadata remain valid after adding launch metadata.
- Paths and process arguments work on every supported target OS.

## Estimate boundary

The 7-day estimate covers package metadata, persistent store layout, runtime
launch contract, direct synchronization gates, rolling procedure, compatibility
guards, rollback behavior, and repository scenarios. It does not cover building
a cross-platform daemon supervisor, production installer, backup system,
database migration tooling, or OS account provisioning. Add that work
explicitly if required.

## Exit criteria

- A package unambiguously describes how to launch its configured instances.
- Versioned package replacement cannot overwrite either persistent store.
- The rolling sequence and rollback are executable and tested.
- The limitations of primary-only machines are visible.
- No step depends on Master/Slave service roles.
- `task all` passes.
