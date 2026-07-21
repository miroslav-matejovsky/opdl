# Stage 01: Restore the validation gate

**Effort:** Small. **Complexity:** Low. **Depends on:** nothing.

First, because every stage after this one changes behavior and none of them can be
trusted without it.

## Intent

`task all` must again prove that a built package starts, binds its endpoints, and
transfers ownership. Today it proves that the code compiles and that unit tests
pass with `-short`.

## Current state

`Taskfile.yml:91,97` has two entries commented out:

```yaml
      # - task deadcode
      ...
      # - task scenarios
```

So `task all` runs tidy, vet, fmt, lint, arch, `test -short`, the .NET SDK build,
and blueprint validation. It starts no process and binds no socket. The scenarios
and the platform integration tests both live in `taskfile/scenarios.ps1`.

This matters more now than it did when they were disabled. The vocabulary work
that preceded this plan was compiler-checked by construction: a rename either
compiled or it did not. Stages 04 through 07 change what the platform binds, reads,
and connects to, and none of that is visible to a compiler.

`deadcode` matters for a smaller but real reason: these stages delete paths, and an
unreferenced function that used to be the only caller of another is how a
half-finished migration hides.

## Target

Both entries enabled, and the suite green.

If either cannot be re-enabled today, this stage must say why in writing and the
plan's remaining stages must state how they are validated instead. A gate that is
off without a recorded reason becomes a gate nobody remembers turning off.

## Known obstacle: a flaky scenario

`TestFourMachineStorageTopologyAndFailure` has failed once under full-suite load
and passed alone and on the next full run. The signature is
`event_fabric.projector_attach_retry` and `handler_attach_retry` with
`context deadline exceeded`, immediately after the scenario's deliberate storage
kill.

It is recorded in `docs/backlog/event-fabric.md` as evidence for the open decision
there. It is not caused by this plan and it must not be the reason the gate stays
off. If it is still flaky, quarantine that one test explicitly rather than
disabling the whole suite.

## Decisions

**D1.** Re-enable both, or only `scenarios`? Recommendation: both. `deadcode` is
cheap and this plan deletes code.

**D2.** If the four-machine scenario is still flaky, quarantine it or fix it first?
Recommendation: quarantine with a comment pointing at the backlog entry. Fixing it
is a separate investigation and blocking this plan on it costs more than it saves.

## Work

1. Uncomment `- task deadcode` and `- task scenarios` in `Taskfile.yml`.
2. Run the full suite and fix what it finds.
3. If the four-machine scenario flakes, apply D2.

## Validation

- `task all` passes with both entries enabled.
- The run includes at least one scenario that starts a real package and one that
  exercises ownership transfer.
