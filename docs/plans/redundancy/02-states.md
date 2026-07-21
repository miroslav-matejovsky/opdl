# Stage 02: Active and Standby states

**Effort:** Small. **Risk:** Low. **Depends on:** stage 01, and stage 04 for what
one of its state combinations means.

## Status: done

`task all` passes. The role and state combination table lives in
`docs/operations/monitoring.md` only; `status.go`, `state.go`, and
`troubleshooting.md` point at it rather than repeating it. D1 and D2 answered as
recommended: nothing renamed, no states collapsed. D3 deferred to stage 04.

`troubleshooting.md` gained one symptom section, "The standby service is the one
serving", because that is the question the fixed-role model makes an operator ask
and nothing answered it. It states the persistence explicitly, so stage 04 changes
one paragraph rather than adding a section.

Mostly documentation. The states already exist and are already named correctly;
what is missing is a stated distinction between a role and a state.

## Intent

There are two operational states: **Active** and **Standby**.

A role says which instance a process is. A state says what it is doing now. Under
the Preferred Primary policy the Primary Instance is normally Active and the
Standby Instance is normally Standby, but the interesting cases are exactly the
ones where they differ.

## Current state

`platform/internal/redundancy/state.go:37` defines six lifecycle states:

| State | Meaning |
| --- | --- |
| `starting` | launched, not yet contending |
| `standby` | connected and caught up, not owning |
| `activating` | owns, composing active resources |
| `active` | owns and serving |
| `stopping` | releasing during clean shutdown |
| `failed` | stopped on an error |

Two of these, `active` and `standby`, are the operational states of the target
vocabulary. The other four are transitional.

## The collision worth naming

`standby` is both a role and a state, and a status file carries both:

```json
{ "role": "standby", "state": "standby", ... }
```

Under the target vocabulary this is legitimate: a Standby Instance normally
operates in the Standby state. But it is a genuine readability hazard, because
the combinations that matter operationally are the ones where the two differ:

| role | state | Meaning |
| --- | --- | --- |
| `primary` | `active` | normal operation under the Preferred Primary policy |
| `standby` | `standby` | normal operation |
| `standby` | `active` | **failover has occurred**, the machine is running on its Standby Instance |
| `primary` | `standby` | the Primary Instance is available but does not own; failback has not happened yet |
| `primary` | `active` and `standby` `active` | must never occur, and cannot |

The third and fourth rows are the ones an operator needs to recognize instantly,
and neither is obvious from a status file that repeats the word `standby`.

**The fourth row's meaning depends on stage 04.** Under today's behavior, and
under a `manual` failback policy, `role=primary, state=standby` is a persistent
steady state: a machine can sit there indefinitely with a healthy, idle Primary
Instance, because ownership returns only when an operator stops the Active
Standby. Under an `automatic` policy it is transient, and a long dwell time is a
fault. The monitoring runbook must describe whichever is chosen, and say whether
the row warrants an alert.

## Decisions

**D1. Rename either the role values or the state values to remove the
collision?**

Recommendation: **no.** Both sets are correct in the target vocabulary, both are
operator-visible contracts, and renaming one to avoid an overlap would introduce a
word the vocabulary does not define. Document the distinction instead, and make
the differing combinations explicit in the monitoring runbook.

**D2. Collapse the four transitional states into Active and Standby?**

Recommendation: **no.** `activating`, `stopping`, and `failed` carry information
operators use during failover and shutdown, and `starting` distinguishes a process
that has not yet contended from one that contended and lost. Keep the graph;
document that Active and Standby are the two operational states and the rest are
transitional.

**D3. Should `Status.Promotable` be renamed?**

Under the vocabulary it means "ready to assume Primary Ownership". `promotable` is
not itself banned vocabulary, but it belongs to the promotion language stage 05
removes.

It is a status-file field read by deployment tooling, asserted in scenarios, and
quoted in three runbooks, so renaming it is a wider break than the event names in
stage 06.

Recommendation: rename to `failover_ready` in stage 05 with the rest of the
promotion vocabulary, not here. Flagged in both stages so it is not lost.

## Work

1. `platform/internal/redundancy/state.go`: document Active and Standby as the two
   operational states and the rest as transitional, in the type's doc comment.
2. `platform/internal/redundancy/status.go`: document the role and state fields as
   different axes, with the four-row table above.
3. `docs/operations/monitoring.md`: add the role and state combination table, and
   call out `standby`/`active` as the signal that failover has occurred.
4. `docs/operations/troubleshooting.md`: use the combinations when describing how
   to tell a failed-over machine from a normal one.
5. `docs/01-architecture.md`: state the two operational states by name in the
   local redundancy section.

## Validation

- No code change, so `task all` passing is a regression check only.
- A reader of `docs/operations/monitoring.md` can determine from a status file
  alone whether a machine has failed over.
