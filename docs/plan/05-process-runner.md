# Stage 5: extract the process runner

## Goal

Collapse the three duplicate process lifecycles in the harness into one tested
`utils` package. Behavior does not change. The parallel suite from stage 4 is
the proof.

## Why

The harness runs child processes three different ways, and all three implement
the same thing.

| | `machine` | `managedProcess` | `process` |
| --- | --- | --- | --- |
| declared | `harness_test.go:432` | `harness_test.go:475` | `harness_test.go:829` |
| started | `start`, `:733` | `startManaged`, `:503` | `startProcess`, `:844` |
| output buffer | `syncBuffer` | `syncBuffer` | `syncBuffer` |
| process tree | `processtree.Owner` | `processtree.Owner` | `processtree.Owner` |
| exit signal | `done` chan | `done` chan | `finished` chan |
| exit error | `err` field | `err` field | `err` field |
| alive check | `running`, `:491` | `running`, `:528` | `exited`, `:876` |
| force stop | `stop`, `:768` | `kill`, `:537` | via cleanup, `:856` |
| collect | `wait`, `:788` | `exitResult`, `:565` | `wait`, `:870` |
| snapshot logs | `logs`, `:780` | `logs`, `:560` | `logs`, `:888` |

Every row is the same idea with a different name and a slightly different
contract. `machine.logs` stops the process before reading; `process.logs` does
not; `managedProcess.logs` does not, but `exitResult` blocks first. Those
differences are real and documented, but they are differences in what the
caller wants, not in what a process is. One type can express all three.

The duplication also carries a subtle hazard that stage 4 makes worse:
`machine.stopped` (`:456`) is a plain bool written by `stop` and read by
`running`, while `done` is closed on a goroutine. The other two types do not
have that field at all and rely only on the channel. Consolidating on the
channel-only model removes the field and the question of whether it needed a
mutex.

## Design

New package `utils/procrun`.

```go
// Process is a child command under test control: started, observable while it
// runs, and collectable once it exits.
type Process struct { ... }

// Start runs cmd inside a kill-on-close process tree, capturing stdout and
// stderr into one buffer.
func Start(cmd *exec.Cmd) (*Process, error)

func (p *Process) PID() int
func (p *Process) Running() bool          // non-blocking
func (p *Process) Logs() string           // snapshot, safe while running
func (p *Process) Kill() error            // force stop, returns once exited
func (p *Process) Stop() error            // graceful, caller bounds the wait
func (p *Process) Wait() (string, error)  // blocks, returns complete output
```

Notes on the contract:

- `Logs` is always a snapshot and never stops the process. The current
  `machine.logs` behavior of force-stopping first is a scenario decision, so it
  stays in the harness as an explicit `stop` then `Logs` at the two call sites
  that want it (`scenarios/build_and_run_test.go:69` and
  `scenarios/two_machine_eventfabric_test.go:66`). Hiding a process kill inside
  a method named `logs` is the kind of surprise that costs an afternoon.
- `Wait` returns the complete output because it joins the exec copier
  goroutines first. That invariant is the reason all three current types
  document the same thing, and it belongs in one doc comment.
- `Stop` sends the platform's graceful signal via `processtree` and returns
  immediately. Bounding the wait is the caller's decision:
  `managedProcess.stopGracefully` (`harness_test.go:545`) bounds it with
  `apiWaitTimeout` and falls back to a kill, which is a scenario policy, not a
  process property.
- `procrun` owns the `syncBuffer` as an unexported type. Nothing outside needs
  it.

`utils/procrun` sits on top of `utils/processtree`, which already handles the
Windows job object and the graceful signal. This stage adds the lifecycle
layer above it, it does not reimplement it.

## Changes

`utils/procrun/` (new)
- `procrun.go`, `syncbuf.go`, `doc.go`.
- Tests driving real child processes. A test binary compiled from a small
  `testdata` main, or `os.Args[0]` re-invoked with an environment marker, gives
  a child that can print, exit with a code, hang, or ignore a graceful signal.
  Cover: output capture, exit code propagation, `Running` before and after
  exit, `Wait` returning complete output, `Wait` being safe to call twice,
  `Kill` on an already-exited process, and `Logs` concurrent with a writing
  child under `-race`.

`scenarios/harness_test.go`
- Delete `syncBuffer` (`:413`), `process` (`:829`), `startProcess` (`:844`),
  and `managedProcess`'s lifecycle half (`:475` to `:568`).
- `machine` keeps its identity fields and its scenario behavior (`restart`,
  `statusPath`, `readStatus`, `waitStatus`) and embeds a `*procrun.Process` for
  the rest.
- `managedProcess` shrinks to a role name plus a `*procrun.Process`, keeping
  `stopGracefully` and `waitStatus` because both are scenario policy.
- `runCommand` (`:217`) becomes `procrun.Start` followed by `Wait`.

`scenarios/dotnet_sdk_e2e_test.go`, `scenarios/warm_standby_test.go`
- Follow the renames. No logic changes.

## Risks

- **Cleanup semantics.** Each current type registers `t.Cleanup` differently:
  `startManaged` registers `p.kill` (`:522`), `start` registers `m.stop`
  (`:749`), `startProcess` registers a kill-then-wait closure (`:856`).
  `procrun.Start` must not register cleanup itself, because it does not take a
  `*testing.T` and should not. Each harness call site registers its own, which
  keeps the ownership explicit and keeps `procrun` usable outside tests.
- **Windows behavior.** The job object handling lives in `processtree` and is
  not being changed. Run the suite on Windows before and after, since that is
  where a process-tree regression shows up.

## Verification

- `task test` covers `procrun` in the fast gate, including under `-race`.
- `task scenarios` passes, parallel, three times.
- Force a failure in each scenario family and confirm the diagnostics still
  contain process output. This refactor touches every path that collects logs,
  and a silent loss of diagnostics would only be discovered during the next
  real failure.

## Done when

- `scenarios/harness_test.go` has one process abstraction, not three.
- `syncBuffer` no longer exists in the `scenarios` package.
- `utils/procrun` has tests that run under `-short`.
</content>
