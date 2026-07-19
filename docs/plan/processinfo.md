# `utils/processinfo`

## Functionality

Read operating-system process information behind one portable API. The initial
scope is resident memory in bytes for a process ID.

## Extraction source and consumers

- Move `processMemoryBytes` from `scenarios/memory_linux_test.go`,
  `memory_windows_test.go`, and `memory_other_test.go`.
- Keep warm-standby memory measurements, baselines, assertions, and reporting in
  `scenarios`.
- Do not add CPU, tree traversal, or monitoring abstractions until a real
  consumer needs them.

## Package boundary

Expose an operation equivalent to `ResidentBytes(ctx, pid) (uint64, error)`.
Validate the PID early and propagate cancellation. Prefer native operating-system
APIs where they are stable and reasonably small. If an operating system needs an
external command fallback, document that dependency and include its stderr or
exit error without losing the PID context.

Parsing `/proc`, command output, or native structures is internal. Return bytes
on every operating system. Do not expose Linux `kB` or command-specific output.

## Tests and documentation

- Unit-test parsers with fixed fixtures, including malformed and missing fields.
- Read the current test process and require a positive result without asserting
  an unstable exact value.
- Reject invalid and missing PIDs with useful errors.
- Verify context cancellation for implementations that may block.
- Run platform-specific tests under their matching build tags.

## Refactoring steps

1. Create the portable API and platform-specific implementations.
2. Move parser tests before changing the scenario.
3. Replace the scenario helper with `processinfo.ResidentBytes`.
4. Remove the three scenario build-tag files.

## Completion criteria

The package reports bytes consistently, does not contain OPDL measurement policy,
all supported operating systems have real coverage, and `task all` passes.
