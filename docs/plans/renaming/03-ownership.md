# Stage 03: Primary Ownership vocabulary

**Effort:** Medium. **Risk:** Low. **Depends on:** stage 01, and stage 07 if that
lands first.

The largest mechanical stage. Entirely compiler-checked.

## Intent

The mechanism that guarantees only one instance can be Active is **Primary
Ownership**. Its operations are **Ownership Acquisition**, **Ownership
Validation**, **Ownership Release**, and **Ownership Transfer**.

The codebase calls it the **fence**, a word borrowed from distributed-systems
literature and absent from the target vocabulary.

## Current state

`fence` appears roughly 180 times in non-test code and 40 times in documentation.

| File | Occurrences |
| --- | ---: |
| `platform/internal/app/runtime.go` | 51 |
| `platform/internal/redundancy/fence.go` | 37 |
| `builder/internal/blueprint/topology.go` | 28 |
| `docs/01-architecture.md` | 15 |
| `platform/deployment/deployment.go` | 13 |
| `docs/operations/upgrade.md` | 11 |
| `builder/deployment/deployment.go` | 8 |
| `docs/operations/troubleshooting.md` | 7 |
| `builder/internal/resolve/resolve.go` | 5 |
| remaining docs | 7 |

The word also carries a claim the current documentation is careful to qualify:
`platform/internal/redundancy/doc.go` has a "Fencing scope" section explaining
that the mutex is *not* a fencing token. Removing the word removes the need for
that qualification, which is a small design benefit rather than only a rename.

## Target

| Now | Target |
| --- | --- |
| `redundancy.Fence` | `redundancy.Ownership` |
| `redundancy.OpenFence` | `redundancy.OpenOwnership` |
| `Fence.TryAcquire`, `Fence.Acquire` | unchanged, these are Ownership Acquisition |
| `Fence.Release` | unchanged, this is Ownership Release |
| `Fence.Held` | unchanged |
| `Fence.Name` | unchanged |
| `redundancy.Acquisition` | unchanged |
| `platform/internal/redundancy/fence.go` | `ownership.go` |
| `fence_test.go`, `fence_process_test.go` | `ownership_test.go`, `ownership_process_test.go` |
| `awaitFence`, `fenceResult`, `fenceDone` | `awaitOwnership`, `ownershipResult`, `ownershipDone` |
| descriptor `fence.object` | see stage 07 |

`utils/winmutex` keeps its name and its internal language. It is a generic
Windows primitive with no domain vocabulary in it, and it should stay that way so
the domain can be renamed without touching it.

### Documentation language

- "machine fence" becomes "Primary Ownership".
- "fence holder" becomes "the instance holding Primary Ownership", or "the Active
  instance" where the state is what matters.
- "Fencing scope" in `redundancy/doc.go` becomes "Ownership scope", and the
  section is shortened: it exists to disclaim a word that will no longer be used.
- The historical note that ownership replaced an OS file lock stays. It explains
  why `instance_dir` no longer takes part in ownership, which is still a live
  operational fact.

## Ownership Validation

The vocabulary names four ownership operations. Three exist. **Ownership
Validation does not.**

It corresponds to verifying that an ownership object this process opened but did
not create carries the security descriptor the platform would have set, which
`utils/winmutex/doc.go` currently records as not implemented and `.todo` tracks as
hardening.

Recommendation: do not implement it in this stage. Name it in the documentation as
a defined operation that is not yet implemented, so the vocabulary is complete and
the gap is visible rather than silently absent.

## Decisions

**D1. `Ownership` or `PrimaryOwnership` as the type name?**

`redundancy.Ownership` reads better at call sites (`redundancy.OpenOwnership`),
and the package already scopes it. `PrimaryOwnership` matches the vocabulary term
exactly but stutters as `redundancy.PrimaryOwnership`.

Recommendation: `Ownership` for the type, "Primary Ownership" in prose and
documentation. Note the deliberate difference so a reader does not treat it as
drift.

**D2. Keep the historical file-lock explanation?**

Recommendation: yes, in `redundancy/doc.go` and `docs/01-architecture.md` only.
It explains why `instance_dir` is no longer part of ownership, which operators
still need. Remove it from everywhere else.

## Work

1. Rename the type, constructor, files, and internal identifiers in
   `platform/internal/redundancy`.
2. Update `platform/internal/app/runtime.go`, the heaviest consumer.
3. Update `platform/internal/app/doc.go` and `redundancy/doc.go`.
4. Update `docs/01-architecture.md`, `docs/operations/*`.
5. Leave event names alone; stage 06 handles them together with the runbooks.
6. Leave the descriptor field alone if stage 07 has not landed; otherwise follow
   its shape.

## Validation

- `task all` passes. The rename is compiler-checked, so the risk is in
  documentation, not code.
- `grep -ri fence` returns nothing outside the two historical notes agreed in D2
  and `utils/winmutex`'s own explanation of what it replaced.
