# Stage 6: Packaging and upgrades

Estimate: 5 person-days.

## Objective

Make machine packages self-describing for two process launches and define a
repeatable rolling upgrade that keeps one active compatible instance available.

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
   temporary path, or mutable working file. Shared read-only descriptor and
   binary content is allowed.
6. Document and automate the rolling sequence where possible:
   verify secondary ready, drain and stop primary N, start primary N+1, wait
   ready and compatible, drain and stop secondary N, start secondary N+1, wait
   ready, then verify both.
7. Define rollback before the first replacement. If primary N+1 fails readiness
   or compatibility, stop it and restart primary N while secondary N stays
   active. Do not continue to secondary.
8. Add compatibility guards. A process must fail before serving if descriptor,
   fabric protocol, or registration record versions cannot coexist with the
   running sibling during the supported N/N+1 window.
9. Define primary-only upgrade behavior honestly. A machine with secondary
   disabled cannot have process-level zero downtime on that machine. Package
   output and documentation must state this consequence at build and operation
   time.
10. Add a builder plan/manifest warning for explicit secondary disablement so
    the resource choice is visible before deployment.

## Rolling-upgrade guarantees

The target guarantee is:

- API calls through the redundant SDK continue while either process is stopped.
- New registrations can be created, queried, and accepted while one process is
  stopped, subject to other machines being available.
- Acknowledged state survives each single-process replacement.
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
- Both entries start from an unpacked package with no generated local edits.
- A primary-only package contains no secondary launch entry and emits the
  approved warning.
- Rolling restart preserves SDK traffic and acknowledged registration state.
- Failure of primary N+1 readiness triggers rollback without stopping secondary
  N.
- Unsupported mixed versions fail before public readiness.
- Checksums and release metadata remain valid after adding launch metadata.
- Paths and process arguments work on every supported target OS.

## Estimate boundary

The 5-day estimate covers package metadata, runtime launch contract, rolling
procedure, compatibility guards, rollback behavior, and repository scenarios.
It does not cover building a cross-platform daemon supervisor or production
installer. Add that work explicitly if required.

## Exit criteria

- A package unambiguously describes how to launch its configured instances.
- The rolling sequence and rollback are executable and tested.
- The limitations of primary-only machines are visible.
- No step depends on Master/Slave service roles.
- `task all` passes.
