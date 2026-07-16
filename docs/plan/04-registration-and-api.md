# Stage 4: Registration and API

Estimate: 6 person-days.

## Objective

Allow either active platform instance to execute the same machine-scoped
registration work. Preserve progress and idempotency across endpoint failover
without exposing platform process redundancy as a service role.

## Recommended semantics

Registration acceptance remains one vote per expected machine. Primary and
secondary share the same machine identity and race to create that machine's
idempotent confirmation. Either may commit a request that originated on their
machine after all expected machines have confirmed.

A registration origin remains machine plus machine IP. It does not include the
platform instance. Posting the same payload to primary and secondary is
therefore the same proposal, not a conflict. The status route can be served by
either instance on the origin machine.

## Implementation steps

1. Separate fabric member identity from registration voter identity. Build the
   expected registration machine set by grouping descriptor instances by
   machine, not by blindly using `Fabric.Members()` as the acceptance set.
2. Keep proposal origin and fingerprint machine-scoped. Verify that instance
   name is absent from proposal equality and fingerprint fields.
3. Keep confirmation storage keys machine-scoped. Let primary and secondary
   race on the same create-if-absent key. Record which process performed the
   transition as event metadata or a non-semantic record field approved by the
   compatibility decision.
4. Allow status lookup on either local instance when the proposal originated on
   their machine. Preserve 404 when querying a different machine.
5. Allow either local instance to create immutable acceptance evidence for a
   proposal originating on its machine. Existing create/swap idempotency and
   contender repair must make concurrent reconcilers safe.
6. Ensure a process being offline does not add a pending acceptance entry. A
   whole expected machine being absent still keeps the request pending, as it
   does today.
7. Apply the Stage 0 public API decision. Recommended: rename the machine-level
   progress JSON property to `platform_machines` and its Go/.NET type to
   `PlatformMachineRegistrationStatus`.
8. Add platform instance identity to confirmation, rejection, acceptance, and
   fabric operational events where it helps identify the process that stated
   the fact. Keep `origin_machine`, `confirming_machine`, and service `role`
   fields machine/service-scoped.
9. Update record versions using the approved N/N+1 rolling policy. New code must
   read records written by the immediately previous rolling-compatible version,
   or the upgrade must use a dual-readable encoding. Do not bump a version and
   make mixed processes reject each other's valid records.
10. Regenerate OpenAPI Markdown, OpenAPI YAML, and the generated Kiota client
    after the public model is final. Never hand-edit generated SDK files.
11. Update registration package documentation and `docs/02-registration.md` in
    the same stage.

## Rejection policy

Validation is currently deterministic for a given record and deployment. Keep
it deterministic across primary and secondary. If mixed versions could produce
different answers, that pair is not rolling-compatible and startup or packaging
must reject the upgrade before traffic is shifted.

Do not invent a rule where one instance acceptance overrides another instance
rejection. Machine-scoped create-if-absent means the first machine decision is
the decision, so compatibility must ensure both versions would write the same
status.

## Tests

- Posting to primary and retrying the identical request to secondary is
  idempotent and produces one contender.
- Posting different data for the same key through the sibling remains a
  conflict.
- Status for a same-machine origin is available through both instances.
- Status through another machine remains 404.
- With primary down, secondary confirms and commits new requests for its
  machine. Repeat with secondary down.
- Concurrent reconcilers on primary and secondary create one semantic machine
  confirmation and one acceptance marker.
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
- Failover does not change proposal identity or create false conflicts.
- Public status expresses machine progress accurately.
- Primary/secondary labels and Master/Slave roles are distinct in types, JSON,
  documentation, and tests.
- Generated artifacts are current.
- `task all` passes.
