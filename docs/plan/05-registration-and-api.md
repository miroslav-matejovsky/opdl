# Stage 5: Registration and API

Estimate: 9 person-days.

Complexity: high. Two independent writers must converge without cross-store
atomic operations.

## Objective

Allow either active platform instance to execute the same machine-scoped
registration work over merged durable facts. Preserve progress and idempotency
across endpoint failover without exposing platform process redundancy as a
service role or depending on Olric to synchronize the local pair.

## Recommended semantics

Registration acceptance remains one vote per expected machine. Primary and
secondary share the same machine identity. Each may durably create the same
semantic machine confirmation in its own store. Stable fact IDs and
deterministic merge rules collapse those copies into one machine vote. Either
may commit a request that originated on their machine after all expected
machines have confirmed.

A registration origin remains machine plus machine IP. It does not include the
platform instance. Posting the same payload to primary and secondary is
therefore the same proposal, not a conflict. The status route can be served by
either instance on the origin machine.

The durable source of truth is the union of the process's writable local store
and the sibling store opened read-only. The direct synchronizer copies that
union into both local stores. Olric exchanges facts with other machines, but no
Olric subscription or membership event is part of same-machine convergence.

## Implementation steps

1. Separate fabric member identity from registration voter identity. Build the
   expected registration machine set by grouping descriptor instances by
   machine, not by blindly using `Fabric.Members()` as the acceptance set.
2. Keep proposal origin and fingerprint machine-scoped. Verify that instance
   name is absent from proposal equality and fingerprint fields.
3. Persist a submitted proposal to the selected instance's store before
   returning `202`. Use one deterministic proposal fact ID so an SDK retry to
   the sibling is idempotent even when local synchronization has not copied the
   first write yet.
4. Keep confirmation fact IDs machine-scoped. Primary and secondary may create
   the same semantic confirmation in their own stores. Require identical
   semantic content for one fact ID and fail on an ID/content mismatch. Record
   the writing instance only as non-semantic provenance or event metadata.
5. Allow status lookup on either local instance when the proposal originated on
   their machine. Before returning `404` or a state older than a known local
   proposal, perform a bounded direct refresh from the sibling reader. Preserve
   `404` when querying a different machine.
6. Allow either local instance to create immutable acceptance evidence for a
   proposal originating on its machine. Make acceptance identity and merge
   deterministic if both reconcilers create it concurrently. Do not use a
   cross-store create-if-absent result as proof that only one fact exists.
7. Ensure a process being offline does not add a pending acceptance entry. A
   whole expected machine being absent still keeps the request pending, as it
   does today.
8. Separate durable immutable facts from fabric projections. Persist facts
   received from other machines before using them to expose a newly accepted
   status. Rebuild mutable request, accepted, and conflict views from durable
   facts during startup and reconciliation.
9. Publish local durable facts for site-wide exchange using idempotent fabric
   operations. Consumption and periodic scans may use the fabric, but paired
   local stores must converge correctly with Olric unavailable.
10. Apply the Stage 0 public API decision. Recommended: rename the machine-level
   progress JSON property to `platform_machines` and its Go/.NET type to
   `PlatformMachineRegistrationStatus`.
11. Add platform instance identity to confirmation, rejection, acceptance, and
   fabric operational events where it helps identify the process that stated
   the fact. Keep `origin_machine`, `confirming_machine`, and service `role`
   fields machine/service-scoped.
12. Update record and durable store versions using the approved N/N+1 rolling
    policy. New code must read records written by the immediately previous
    rolling-compatible version, or the upgrade must use a dual-readable
    encoding. Do not bump a version and make mixed processes reject each
    other's valid records.
13. Regenerate OpenAPI Markdown, OpenAPI YAML, and the generated Kiota client
    after the public model is final. Never hand-edit generated SDK files.
14. Update registration package documentation and `docs/02-registration.md` in
    the same stage.

## Rejection policy

Validation is currently deterministic for a given record and deployment. Keep
it deterministic across primary and secondary. If mixed versions could produce
different answers, that pair is not rolling-compatible and startup or packaging
must reject the upgrade before traffic is shifted.

Do not invent a rule where one instance acceptance overrides another instance
rejection. Deterministic validation and fact identity must ensure compatible
primary and secondary versions produce the same semantic machine decision. A
fact ID/content mismatch is corruption or an incompatible version and must fail
visibly rather than use whichever local file was read first.

## Tests

- Posting to primary and retrying the identical request to secondary is
  idempotent and produces one semantic contender, including before periodic
  sibling synchronization runs.
- Posting different data for the same key through the sibling remains a
  conflict.
- Status for a same-machine origin is available through both instances.
- Status through another machine remains 404.
- With primary down, secondary confirms and commits new requests for its
  machine. Repeat with secondary down.
- Concurrent reconcilers on primary and secondary create one semantic machine
  confirmation and one semantic acceptance marker after direct store merge.
- Primary and secondary stores converge while Olric is unavailable. Starting
  Olric later exchanges the already durable facts with other machines.
- A response lost after the primary durably stores a proposal can be retried to
  secondary after primary is hard-killed. Secondary reads the primary store and
  does not create a conflict.
- A fact ID carrying different content in the two stores fails reconciliation
  with both store identities in the error.
- A redundant machine contributes one machine progress entry, not two.
- A primary-only machine has the same acceptance semantics.
- A mixed site waits for every machine but never waits merely for a stopped
  sibling process.
- Service role validation still accepts only optional `Master` or `Slave` and
  never accepts `primary` or `secondary` as a service role.
- Event payloads identify both the machine decision and the process that stated
  it without field-name ambiguity.
- N and N+1 record readers pass compatibility fixtures selected in Stage 0.

## Exit criteria

- Either instance provides complete registration behavior while its sibling is
  absent.
- Both local stores converge without Olric-mediated synchronization.
- Failover does not change proposal identity or create false conflicts.
- Public status expresses machine progress accurately.
- Primary/secondary labels and Master/Slave roles are distinct in types, JSON,
  documentation, and tests.
- Generated artifacts are current.
- `task all` passes.
