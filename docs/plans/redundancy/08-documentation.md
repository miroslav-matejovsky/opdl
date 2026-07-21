# Stage 08: Documentation, requirements, and scope

**Effort:** Small. **Complexity:** Low. **Depends on:** stages 02-07.

Last, so the runbooks are rewritten once against the finished architecture rather
than four times as it changes.

## Intent

The documentation describes the platform that exists, states the vocabulary rule
permanently, and records the two things about filesystem state and consensus
vocabulary that a future cleanup pass would otherwise get wrong.

## Work

### 1. Write down what the vocabulary rule does not govern

This is the item most likely to cause damage if it stays unwritten. The banned
words appear in two places where they are accurate:

- **NATS and JetStream internals.** The site journal is a Raft group with a
  metadata leader, elections, and quorum. That is what it is.
- **The registration domain**, which uses the words to state that registration
  deliberately has *no* leader and no quorum.

`docs/01-architecture.md` states the rule: the vocabulary governs how the
platform's own local high availability is described. It does not govern a
third-party consensus protocol the platform depends on, nor a statement that some
other part of the system does not use one.

Without this, the next person doing a vocabulary pass renames accurate text and
makes the documentation wrong.

### 2. Rule on filesystem state

The goal says the platform uses no lock files, marker files, shared filesystem
state, or filesystem-based coordination (goal:44).

Ownership satisfies this: it is a kernel object and the filesystem takes no part.
The status files in `instance_dir` need an explicit ruling, because they are
filesystem state that both instances write and a reader could mistake for
coordination.

The ruling, to be stated in `platform/internal/redundancy/doc.go` and the
monitoring runbook: **status files are reporting, not coordination.** Nothing reads
them to make a decision. Deleting every status file must leave ownership and
activation unaffected, and if that is ever untrue it is a defect against the goal.

Verify it rather than asserting it. If any code path reads a status file to decide
anything, that is this stage's real finding and it belongs in whichever stage owns
that path.

### 3. Rewrite the runbooks for the finished architecture

By this point the following have all changed and the four runbooks in
`docs/operations/` refer to the old shape:

| Change | Stage |
| --- | --- |
| States are Active and Passive | 02 |
| Events are `platform.ownership_*` | 03 |
| Each instance has its own API address, and both are reachable | 04 |
| Each instance runs its own journal, doubling storage footprint | 05 |
| Failback has a configured policy | 06 |
| Services receive lifecycle notifications | 07 |

`docs/operations/upgrade.md` needs the most work: it assumes one endpoint per
machine, ownership that stays where it is put, and a rolling upgrade that stage 03
makes unsafe for one release.

### 4. Requirements draft

`docs/drafts/requirements.md` section 6 is titled "Redundancy and Leader Election".
The platform has no leader election and this plan is the reason. Reword the section
against the goal architecture.

### 5. Architecture document

`docs/01-architecture.md` needs the site topology updated for two servers per
storage machine, the replica placement constraint from stage 05, and the failback
policy from stage 06.

## Decisions

**D1.** Does `redundancy-goal.md` become the architecture document, or stay a plan
input that `docs/01-architecture.md` is written against? Recommendation: stay an
input. It is a statement of intent; the architecture document describes what was
built, and the two drifting apart is normal and useful information.

**D2.** Is there a measurement pass at the end? The old measurement tables in
`docs/backlog/redundancy.md` record failover and failback timings from the
single-server architecture. After stage 05 they describe a platform that no longer
exists. Recommendation: re-measure and keep both, labelled, because the comparison
is the evidence for whether this plan improved anything.

## Validation

- `task all` passes with the gate enabled.
- No runbook describes a state, event, address, or procedure that no longer exists.
- The vocabulary carve-out is written in `docs/01-architecture.md`, not only in
  this plan.
- Deleting every status file on a running machine changes no behavior.
