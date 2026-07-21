# Stage 03: Finish the ownership rename

**Effort:** Medium. **Complexity:** Medium. **Depends on:** stage 01.

## Intent

`fence` disappears. The mechanism is Primary Ownership, and every place an operator
or an author can see it says so.

## Current state

The internal rename is done: `platform/internal/redundancy` is built around
`Ownership`, `OpenOwnership`, `Acquisition`, and `InstanceRole`. What was left
behind is everything on a contract boundary, because renaming those changes files
people have already written and dashboards people already watch.

Three surfaces still say `fence`:

**The blueprint** (`builder/internal/blueprint/topology.go:86,147-172`):

```hcl
platform {
  fence { namespace = "opdl" }
}
```

with `Fence`, `DefaultFenceNamespace`, `maxFenceNamespace`, `validateFence`, and
`Machine.FenceNamespace()`.

**The deployment descriptor** (`builder/deployment/deployment.go`,
`platform/deployment/deployment.go`): the `Fence` type and the `fence.object`
field, plus the derived name `<namespace>.fence.<digest>` in
`builder/internal/resolve/resolve.go`.

**The operational events** (`platform/internal/app/runtime.go:72,80,96,114,118`):
`platform.fence_open_failed`, `platform.fence_opened`, `platform.fence_waiting`,
`platform.fence_acquired`. Their messages already say "ownership"; only the type
strings lag. Referenced from `docs/01-architecture.md` and four runbooks.

## Target

| Now | Target |
| --- | --- |
| blueprint `fence { namespace }` | `ownership { namespace }` |
| `DefaultFenceNamespace` | `DefaultOwnershipNamespace` |
| `Machine.FenceNamespace()` | `Machine.OwnershipNamespace()` |
| descriptor `Fence{Object}` / `fence.object` | `Ownership{Object}` / `ownership.object` |
| derived `<ns>.fence.<digest>` | `<ns>.ownership.<digest>` |
| `platform.fence_opened` | `platform.ownership_opened` |
| `platform.fence_acquired` | `platform.ownership_acquired` |
| `platform.fence_waiting` | `platform.ownership_waiting` |
| `platform.fence_open_failed` | `platform.ownership_open_failed` |

## Where the ownership block belongs

Under `platform {}`, at machine level. **Not** under `standby {}`.

An earlier plan proposed moving it under `standby` on the reasoning that ownership
only matters when a standby is deployed. The goal architecture settles it the other
way: the mutex is *the only shared resource between instances* (goal:70), so it is
the one thing on a machine that belongs to neither instance. Every other block in
the blueprint is now per-instance precisely because it is owned by one of them.
Putting the shared object inside one instance's block would say the opposite of
what the architecture means.

It also stays correct for a machine with no standby: a single Primary Instance
still acquires ownership, and that acquisition is what makes it Active.

## The one change with a real failure mode

Renaming the derived object from `<ns>.fence.<digest>` to `<ns>.ownership.<digest>`
changes the kernel object two instances contend for.

A machine running one instance on the old name and one on the new name has **two
ownership objects and no contention**. Both instances acquire, both go Active, and
nothing reports an error. That is a silent split brain, and it is exactly the
failure the derived name exists to prevent.

Both instances of a machine must therefore be upgraded together. That is already
what `docs/operations/upgrade.md` describes for a rolling upgrade, but the rolling
upgrade deliberately runs the two instances on different binaries for a window, and
during that window this change is unsafe.

**This stage requires a stop-both-then-start-both upgrade, not a rolling one.** The
runbook must say so for this release specifically.

## Decisions

**D1.** Is anything outside the repo matching on the four event names, such as a
log pipeline or an alert rule? They are the operational contract this stage breaks.
If yes, decide whether to emit both names for one release.

**D2.** Given the split-brain window above, does this stage ship on its own with a
documented full-restart upgrade, or does it wait and ship together with stage 04 or
05, which also need a restart? Recommendation: ship it with stage 04, so the
deployment takes one interruption rather than two.

**D3.** Keep the derived object's `<ns>.<kind>.<digest>` shape at all? It is
already opaque to an operator. Recommendation: keep it. The middle token is what
lets someone reading `Global\` object names tell an ownership object from something
else the platform might name later.

## Work

1. Blueprint: rename the block, the type, the constants, and the accessor.
2. Resolver: rename the derived object's middle token.
3. Both descriptors: rename the type and the JSON field, in lockstep. The
   conformance check in `conformance-tests/deployment-descriptors` fails if only
   one side moves, which is the safety net for this step.
4. Events: rename the four types. Their messages already use the target vocabulary.
5. Update every blueprint and fixture: `examples/customer-a/project.hcl`,
   `builder/internal/blueprint/testdata/project.hcl`,
   `scenarios/testdata/project.hcl.tmpl`, `platform/embedded/deployment.json`.
6. Update `docs/01-architecture.md` and the four runbooks, including the
   full-restart note from D2.

## The prose hazard, again

Same as stage 02, and this word is worse: `fence` is a common English noun and the
codebase used it as one in comments. Sed the identifiers, replace prose with
explicit pairs, then grep and read. Last time this exact rename ran, a
word-boundary sed produced "the exclusive machine ownership", "not a ownership
token", and "machine ownership ownership object opened", all of which compiled.

## Validation

- `task all` passes with the gate enabled.
- `grep -rin fence` returns nothing outside this plan's own history.
- A scenario asserts the four renamed events by their new names.
- Two instances of one machine still contend for one object: the warm standby
  scenario's failover still works, which is what proves the derived name is shared.
