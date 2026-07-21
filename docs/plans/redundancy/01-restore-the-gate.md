# Stage 01: Restore the deadcode gate

**Effort:** Small. **Complexity:** Low. **Depends on:** nothing.

## Status: done

`deadcode` is back in `task all` and the suite passes.

It found one thing immediately: `builder/deployment.Role(standby bool)`, a helper
added with the per-instance descriptor work and never called. The resolver converts
the other direction, comparing against `RoleStandby` directly. The platform's copy
of the same helper *is* used and stayed. Deleted from the builder.

That is the small version of what this gate is for. The stages ahead delete whole
paths — `clientOnly` in stage 05, the shared `address` setting in stage 04 — and an
unreferenced function that used to be the only caller of another is how a
half-finished migration hides.

### Scenarios are still off, deliberately

`# - task scenarios` stays commented out until the redundancy implementation
actually changes. Re-enabling it now would gate this plan on a suite that tests the
single-runtime architecture the plan replaces.

**This is a real reduction in what `task all` proves, and every stage from 04
onward has to account for it.** The gate currently covers tidy, vet, fmt, deadcode,
lint, arch, unit tests with `-short`, the .NET SDK build, and blueprint validation.
It starts no process and binds no socket.

Stages 02 and 03 are compiler-checked renames, so this costs them little. Stages 04
through 07 change what the platform binds, reads, and connects to, and none of that
is visible to a compiler or to a `-short` unit test. Each of those stages states its
own scenario coverage under Validation, and those scenarios are the thing that has
to come back before the plan can be called finished.

### When to turn it back on

Before stage 04 lands, not after. Stage 04 is the first stage that can produce a
machine which starts, reports healthy, and serves the wrong endpoint, which is
precisely the failure a black-box scenario catches and nothing else here does.

The scenarios also need updating for the two-runtime model as part of stages 04 and
05, so re-enabling is not a switch flip: the existing suite asserts one API address
per machine and one NATS server per machine, and both stop being true.

### Known obstacle when it does come back

`TestFourMachineStorageTopologyAndFailure` has failed once under full-suite load
and passed alone and on the next full run. The signature is
`event_fabric.projector_attach_retry` and `handler_attach_retry` with
`context deadline exceeded`, immediately after the scenario's deliberate storage
kill.

Recorded in `docs/backlog/event-fabric.md` as evidence for the open decision there.
It is not caused by this plan, and it must not become the reason the suite stays
off a second time. If it is still flaky, quarantine that one test with a comment
pointing at the backlog entry rather than disabling the suite.
