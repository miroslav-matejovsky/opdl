# `utils/processtree`

## Functionality

Start an `exec.Cmd` inside an operating-system process container, terminate it
gracefully when supported, and force-kill all descendants during cleanup. The
main requirement is that child processes cannot outlive their owner, including
when the owner is canceled or a test times out.

## Extraction source and consumers

- Move `processTree`, `startCommand`, Windows job-object setup, suspended start,
  thread resume, process-group configuration, and portable termination from
  `scenarios/process_windows_test.go` and `process_unix_test.go`.
- Replace direct uses in `scenarios/harness_test.go`.
- Keep scenario output capture, `testing.T` cleanup, OPDL machine lifecycle,
  marker handshakes, and assertions in `scenarios`.
- The builder and conformance command wrappers should use this package only if
  they later need descendant containment. Do not move command-specific arguments
  or error text into the utility.

## Package boundary

Provide a small owner type returned by a start operation. It should expose
graceful termination, forceful tree kill, and resource close. The caller remains
responsible for configuring stdin, stdout, stderr, environment, directory, and
for waiting on `exec.Cmd`.

On Windows, create the child suspended, assign it to a kill-on-close job, then
resume it so descendants cannot escape before assignment. Use a new process
group when graceful console control events are required. On Unix, use a distinct
process group and signal the group so descendants are covered, not only the
direct child. Integrate with `exec.CommandContext` cancellation without allowing
the default direct-child kill to leave descendants behind.

All stop and close operations must be concurrency-safe and idempotent. Define
which errors take precedence when process wait and container cleanup both fail.

## Tests and documentation

- A helper process starts a grandchild; force cleanup terminates both.
- Graceful termination reaches the whole managed group and allows a clean exit.
- Context cancellation cleans up descendants.
- Start failure and assignment failure do not leak a process or OS handle.
- Repeated and concurrent graceful stop, kill, and close calls are safe.
- Windows tests verify the child cannot run before job assignment.
- Unix tests verify a grandchild cannot survive group termination.

Use real helper processes. Keep tests deterministic with pipe or file handshakes,
not sleeps.

## Refactoring steps

1. Build and test `utils/processtree` on Windows and Unix.
2. Replace `runCommand`, managed-process, machine, and background-process tree
   ownership one caller at a time.
3. Preserve scenario-level cleanup and diagnostics after each migration.
4. Delete the scenario OS-specific process files only when every call site uses
   the package.

## Completion criteria

No scenario-owned child or descendant survives cleanup, graceful handover still
uses the intended OS signal, all OS handles are released, the package has no
OPDL or testing-framework dependency, and `task all` passes.
