# Stage 5: Two-phase registration over the platform fabric

Estimate: 6-9 engineer-days.

## Goal

Implement registration as a site-wide two-phase process over the platform
fabric. POST creates a pending request on one origin machine. The request becomes
accepted only after every platform instance in the site's static deployment
topology records acceptance. Clients identify the request by `(unit_type,
unit_id)` and poll status on the origin machine. No client-visible request id is
created.

## Instructions

1. Use three named fabric collections with versioned internal records:

   - `registration-requests`, keyed by the composite unit key, stores the current
     proposal, origin machine/IP, deterministic fingerprint, and terminal
     rejection when present.
   - `registration-confirmations`, keyed by unit key, proposal fingerprint, and
     confirming machine, stores accepted or rejected plus a bounded reason.
   - `registrations`, keyed by the composite unit key, stores only fully accepted
     immutable registration values plus their internal fingerprint.

   Keep collection names and encodings owned by the registration package. The
   fabric API remains domain-neutral.

2. Compute the proposal fingerprint deterministically from the canonical request
   fields plus origin machine and IP. Use it only to prove each confirmation
   refers to the exact pending data. It is internal data, never returned by REST,
   and is not a generated request id.

3. Implement request creation with atomic create-if-absent:

   - A key with no pending or accepted registration creates a pending request,
     emits `platform.registration.requested`, and returns 202.
   - An exact match of request fields, machine, and IP is an idempotent retry. It
     returns 202 and does not emit another requested event.
   - Any mismatch for an existing pending or accepted key returns the typed
     registration-key conflict, preserves all state, and emits
     `platform.registration.conflict` tagged `warning` on the rejecting origin.
   - A different advertised name or role on the same machine is conflict. A
     different machine or IP is also conflict.

   Because there is no caller identity, an identical second service on the same
   machine is indistinguishable from a retry. Document and test this limitation.

4. Add one registration reconciler per platform process. It must:

   - Run one immediate reconciliation after the fabric becomes ready.
   - Periodically enumerate pending requests using a configurable interval.
   - Validate proposal schema, fingerprint, composite key, origin, and conflict
     against accepted state.
   - Record exactly one confirmation for its own descriptor machine and the
     current proposal fingerprint.
   - Emit `platform.registration.confirmed` once when it first records an
     acceptance.
   - Record a bounded rejection and emit `platform.registration.rejected` tagged
     `warning` when validation fails.
   - Remain idempotent across scans and restarts.

   Inject the reconciliation trigger in tests. Do not use sleeps to coordinate
   unit or integration tests.

5. Use the immutable expected member list from the fabric topology as the
   acceptance set. Include the origin platform instance. Do not derive the set
   from currently connected Olric members. A disconnected or not-yet-started
   expected machine keeps the request pending.

6. Aggregate confirmations on the origin machine:

   - Ignore confirmations whose fingerprint does not match the current proposal.
   - If any expected member rejects, expose status `rejected` with its bounded
     reason. Do not create or change an accepted registration.
   - If any expected member has not confirmed, expose status `pending`.
   - Only when every expected member confirms acceptance, atomically create the
     immutable accepted registration under the composite key.
   - The accepted registration write is the single commit point. Status is
     `accepted` only when that exact record exists.
   - Emit `platform.registration.accepted` once after the accepted record is
     created.

   Create-only registration makes this one write sufficient. There is no update,
   replacement, quorum, automatic timeout, or forced acceptance.

7. Implement origin-machine status lookup:

   - Locate by path `unit_type` and `unit_id`; accept no request id.
   - Return 404 if no request exists or if the current platform machine is not
     the request origin.
   - Return one registration view with origin machine/IP, overall `pending`,
     `accepted`, or `rejected`, and the per-instance projection described below.
   - Reconcile current confirmations before responding or rely on the same
     injected coordinator trigger, but never report accepted before the accepted
     registration record exists.

8. Implement one projection used by both status and list:

   - Enumerate `registration-requests`, not only accepted records.
   - For each request, derive overall status: accepted when the exact accepted
     registration exists, rejected when any expected confirmation rejects, and
     pending otherwise.
   - Build `platform_instances` from the complete expected topology. For each
     expected machine, include descriptor machine/IP and accepted or rejected
     confirmation. If no matching confirmation exists, include it as pending.
   - Include a bounded reason only on rejected instance entries and on overall
     rejected status when useful.
   - If overall status is accepted, decode and validate the accepted record. For
     pending or rejected, use the immutable request fields and origin location.
   - Return pending, accepted, and rejected views from `GET /registrations`.
   - Return `[]` only when no registration request exists.
   - Sort registrations by origin `machine`, then `unit_type`, then `unit_id`.
     Sort every `platform_instances` array by machine.

9. Keep backend iterator ownership, byte copying, and Olric errors inside the
   fabric adapter. Registration code depends only on fabric collections and
   expected member identities. HTTP and SDK code must not import fabric or Olric
   types.

10. Wire runtime lifecycle in dependency order:

    - Start fabric.
    - Open registration collections.
    - Start the reconciler and run its initial pass.
    - Start public HTTP.
    - On shutdown, stop HTTP intake, stop the reconciler, drain and close fabric,
      then close events.

    A fabric or reconciler startup failure prevents the public API from starting.

11. Add store and multi-member integration tests covering:

    - POST returns 202 and status remains pending with one expected machine
      offline.
    - Starting the missing machine records its confirmation and allows acceptance.
    - Every expected machine, including origin, has one matching confirmation.
    - A confirmation with a wrong fingerprint cannot approve the pending request.
    - List shows the request as pending before the commit, accepted after the
      commit, and rejected when one member rejects.
    - Per-instance state identifies accepted, rejected, and still-missing
      confirmations for every expected machine.
    - Status lookup works only on the origin machine.
    - Exact retries in pending and accepted states are idempotent.
    - Same-machine different name or role returns conflict and emits the warning
      event.
    - Different machine or IP returns conflict and preserves the accepted record.
    - Concurrent distinct requests for one key produce one winner and conflicts.
    - One explicit member rejection produces rejected status and no accepted
      registration record.
    - Reconciliation is idempotent after a process restart.
    - Shutdown cancels scans and returns bounded errors instead of hanging.

12. Expand the two-machine Go scenario:

    - Build a two-machine blueprint in one site.
    - Start node A only. POST on A must return 202. Wait for A's self-confirmation,
      then require A status and list to show the request pending, with A accepted
      and node B pending because B is expected but offline.
    - Start node B. Poll status only on A until accepted, then verify list shows
      accepted from both nodes, with both platform instances accepted and A's
      origin machine/IP unchanged.
    - Verify status lookup on B returns 404 for A's request.
    - Send an exact retry on A and verify no duplicate phase events.
    - Send the same key with a different advertised name on A and require 409 plus
      a `warning`-tagged conflict event.
    - Send the same key on B and require 409 plus another warning-tagged conflict
      event. Verify the accepted record remains unchanged.

13. Document the consistency and failure semantics:

    - The acceptance boundary is every statically expected platform instance in
      the site, not a quorum and not current live membership.
    - State is in memory and is not replayed after full-site shutdown.
    - Pending may last indefinitely while an expected instance is unavailable.
    - Registration is create-only. Any change requires future explicit removal.
    - `(unit_type, unit_id)` is unique across the whole site fabric and never
      scoped by machine.
    - Different sites have separate fabrics and do not enforce cross-site
      uniqueness in this use case.

14. Run focused registration, fabric, race, HTTP, and multi-machine scenario
    tests, then run `task all`.

## Acceptance

- POST returns 202 for a valid request without claiming registration success.
- The origin status endpoint is the only client confirmation mechanism.
- A request is accepted only after every expected site platform instance records
  a matching acceptance.
- Pending, accepted, and rejected requests all appear in the registration list
  with deterministic per-platform-instance progress.
- No client-visible or randomly generated request id exists.
- Same-machine and cross-machine key conflicts return 409, emit warning-tagged
  conflict events, and preserve state.
- Registration and HTTP packages contain no Olric imports or backend types.
- Authentication, authorization, removal, timeout, quorum, persistence, and
  redundancy remain out of scope.
- `task all` passes.

## Risks and controls

- Collection writes are not one distributed transaction. Treat creation of the
  accepted registration record as the only commit point and make request and
  confirmation writes idempotent.
- A down expected instance blocks acceptance. This is required behavior. Make
  pending status and missing confirmation diagnostics observable in status,
  list, events, and tests without weakening the all-instance rule.
- Periodic reconciliation can duplicate work. Fingerprint-scoped confirmation
  keys and idempotent writes prevent duplicate acceptance.
- Fabric-wide enumeration may become expensive. It is acceptable for the first
  use case. Add indexing or change notifications only after measured need.
