# Stage 08: Scope boundaries and requirements

**Effort:** Small. **Risk:** Low. **Depends on:** nothing.

Settle this stage's carve-out rule **before** executing any other stage. Without
it, a later cleanup pass will "correct" documentation that is already right.

## Intent

The vocabulary rule governs how the platform's own local high availability is
described. It does not govern everything in the repository that happens to use one
of the avoided words.

## The carve-out

A repository-wide search for leader, follower, election, leadership, consensus,
quorum, distributed lock, and leadership token returns **no term describing the
platform's primary/standby architecture.** Every hit is one of two kinds.

### Kind 1: NATS and JetStream internals. Do not change.

| Location |
| --- |
| `platform/internal/eventfabric/nats/doc.go:23,33,93` |
| `platform/internal/eventfabric/nats/journal.go:43,65,135,136` |
| `platform/internal/eventfabric/nats/config.go:180` |
| `builder/deployment/deployment.go:298` |
| `docs/01-architecture.md:178,200,207` |
| `docs/operations/troubleshooting.md:29,64,70` |
| `docs/operations/monitoring.md:64,90` |
| `docs/operations/upgrade.md:88` |
| `docs/backlog/event-fabric.md:19` |

These describe the site journal's Raft replica group, which genuinely is a
distributed consensus system with a metadata leader, elections, and quorum. The
words are accurate and technically necessary. Renaming them would make the
documentation wrong and would obscure a real operational constraint: a
three-storage-node site loses write availability while its metadata group elects a
new leader.

### Kind 2: statements that some part of the system has no leader. Do not change.

`docs/02-registration.md:69,92,162,165` uses the words to state that registration
deliberately has no leader, no quorum, and no leases. Describing an absence is not
adopting the vocabulary, and the statements are load-bearing: they are why
registration behaves as it does.

### Where the rule does apply

Everything describing how the two local instances relate: their roles, their
states, ownership between them, and the transitions between them. That is stages
01 through 07.

## Work item 1: write the carve-out down

Add it to `docs/01-architecture.md` where the local redundancy section meets the
Event Fabric section, as a short note: the platform's local high availability is
Fixed-Role Primary/Standby with no election; the site journal's replica group is a
Raft consensus group with one; the two are different mechanisms at different
scopes and are described in different terms deliberately.

This is a few sentences and it prevents an entire category of future mistake.

## Work item 2: the requirements draft

`docs/drafts/requirements.md` section 6 is titled "Redundancy and Leader Election"
and contains OR-35 through OR-42.

OR-38 is the problem: *"The platform MUST provide leader-election capabilities for
groups of instances participating in coordinated failover."* It merges two
different requirements:

- **Local high availability on one machine.** This is Fixed-Role Primary/Standby
  under a Preferred Primary policy, and it is implemented.
- **Site-wide single-active selection across machines** for client services. A
  different problem with dynamic candidacy and no fixed roles, planned separately.

Recommended treatment:

| Requirement | Action |
| --- | --- |
| Section title | "Redundancy and Ownership" |
| OR-35 redundancy as a platform capability | reword in target vocabulary |
| OR-36 services declare single-active execution | unchanged, belongs to the site-wide feature |
| OR-37 coordinate active-instance assignment | reword, scope to the site-wide feature |
| OR-38 | **split**: local as Fixed-Role Primary/Standby; site-wide as single-active selection |
| OR-39 notification on ownership change | reword in target vocabulary |
| OR-40 autonomous site-local operation | unchanged |
| OR-41 configurable failover behavior | reword; drop "re-election" |
| OR-42 observable redundancy state | reword in target vocabulary |

## Work item 3: the adjacent plan

`docs/plans/leadership/` plans a site-wide feature in which one machine per
`unit_type` hosts the Active client service. It uses leases, expiry, and an epoch
as a fencing token, and candidacy is dynamic.

That feature is **not** Fixed-Role Primary/Standby, and the target vocabulary does
not describe it: there are no fixed roles and ownership must expire. Renaming it to
match would describe it incorrectly, and the epoch really is a fencing token, which
is a term this plan removes from the local architecture precisely because the local
mutex is not one.

Recommendation:

- Rename the directory to `docs/plans/service-single-active/`, so it does not
  claim leadership vocabulary for the platform's own high availability.
- Keep its internal terminology accurate to what it is, and state in its README
  that it is a different mechanism at a different scope from the local Fixed-Role
  Primary/Standby architecture.
- Add the reciprocal note to `docs/plans/renaming/README.md` and
  `docs/plans/fence-ownership/README.md`.

This is the one place where applying the rule literally across the repository
would make the documentation worse, so it should be an explicit decision rather
than a silent exception.

## Decisions

**D1.** Confirm the carve-out as stated: NATS internals and no-leader statements
are out of scope for the vocabulary rule.

**D2.** OR-38 split as proposed, or reword in place and leave the site-wide
requirement implicit?

**D3.** Rename `docs/plans/leadership/`? If yes, to
`service-single-active` or another name. It is referenced from
`docs/plans/fence-ownership/README.md` and from `.todo`.

**D4.** Is `docs/drafts/requirements.md` still authoritative? It is under
`drafts/` and describes capabilities that do not exist. If it is a historical
input rather than a live contract, the reword is cosmetic and could be skipped;
if it is a live contract, the OR-38 split matters.

## Validation

- `docs/01-architecture.md` states the carve-out in one place.
- No stage 01 through 07 change touches any location listed under Kind 1 or
  Kind 2.
- A grep for the avoided vocabulary returns only those locations.
