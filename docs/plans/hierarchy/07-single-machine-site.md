# Step 07: implement the single-machine site slice and registration

| | |
| --- | --- |
| Complexity | High |
| Effort | 5-8 person-days |
| Depends on | 06 |
| Blocks | 08 |

## Goal

Implement the accepted site-distribution contract for one machine and make
registration run end to end. `POST /registrations` returns `202`, the local
machine confirms and accepts, queries answer from the projection, and restart
rebuilds the same answers.

## Why this slice first

- It proves the concrete adapter against the existing `eventfabric` contract.
- It exercises scope routing through the common publisher.
- It removes all current registration stubs before network distribution adds
  another failure boundary.
- The existing scenario harness already builds single-machine deployments.

## Design

1. **Site adapter.** Implement the ADR's single-machine storage. Its publication
   side accepts only site-scoped envelopes from the common publisher flow. Its
   consumer side provides ordered replay-then-follow and durable
   acknowledgement.
2. **Composition.** Do not create a second event model or stamp the payload
   twice. The instance log must receive the same envelope as site storage.
   Define ownership of shared backends clearly so one publisher cannot close a
   file still used by another.
3. **Descriptor.** Add the minimum site-distribution configuration selected by
   the ADR to the blueprint, resolved descriptor, platform config, examples,
   scenario templates, and descriptor conformance checks, including the
   projection lag bound the runtime will need again. Derive whether an instance
   has a journal from the descriptor.
4. **Command path.** Implement `CommandService.Create`: domain validation
   (blank advertised name, unknown role, and the documented `400` reasons),
   proposal-ID derivation (already implemented in `identifiers.go`),
   publish `Proposed` through the common publisher with its site scope, and
   return the receipt. The
   `503 journal_unavailable` path maps from publish failure.
5. **Handler.** Implement `NewHandler` per its contract: consume
   proposals/confirmations, wait for its own projection to have applied the
   input, publish exactly one deterministic consequence (`Confirmed`,
   `Rejected`, `Accepted`). On a single-machine site the expected set is
   `[self]`, so acceptance follows the machine's own confirmation. The
   full flow still exercises every event type.
6. **Runtime site.** Add a site composition to `app`: open the projection, catch
   up, attach the handler for an Active instance, catch up consequences, become
   ready, and expose registration through the existing listener. The previous
   unimplemented stub was removed, so this is written fresh.
7. **Passive side.** A Passive instance follows the site state but attaches no
   domain handler. Add projection-lag readiness back: the failover monitor, its
   `FailoverReadinessChanged` fact, and `redundancy.LagState` were removed with
   the stub and need reintroducing against a real projection.

## Actions

1. Implement adapter and contract tests.
2. Add descriptor, builder, config, conformance, and example changes.
3. Implement `Create`, `NewHandler`, the runtime site composition, and the
   passive follower.
4. Add unit tests for scope refusal, replay, acknowledgement, handler
   determinism, and publication failure.
5. Update `docs/01-architecture.md`, `docs/02-events.md`, and
   `docs/04-registration.md`.
6. Add a Windows scenario for propose, poll, restart, failover, and replay.

## Acceptance criteria

- On a standby-enabled single-machine build, POST returns `202`; polling
  reaches `accepted`; `GET /registrations` and `/conflicts` answer per the
  API contract; a second identical POST is idempotent (same proposal ID); a
  conflicting POST is projected `rejected` with `registration_key_conflict`.
- Kill the active instance: the standby takes ownership, replays, and
  serves the same registration answers.
- Full restart of the machine reconstructs answers from durable site state
  alone.
- The instance event log and site storage contain the same envelope ID for each
  site fact.
- Non-site envelopes never enter site storage.
- No `ErrNotImplemented` remains on the registration path.
- Whether an instance has a journal is read from the descriptor.

## Risks / open questions

- The publisher currently owns its backends and closes them. Sharing lower-level
  storage with a site-specific publisher would create double-close and lifecycle
  ambiguity. Resolve this in composition rather than hiding it in adapters.
- Keep the single-machine adapter limited to the accepted contract. Do not add
  transport features needed only by step 08.
