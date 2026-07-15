# Stage 5: Expose registration conflicts

**Estimate:** 1 to 1.5 days.

## Goal

Make detected and resolved duplicate registration claims visible through the
registration HTTP API and generated .NET SDK.

## API shape

Add `GET /registrations/conflicts` with a response grouped by unit key. Each item
contains:

- `unit_type` and `unit_id`;
- `resolution_status`, initially `resolved` once a winner is derivable;
- the winning proposal, including origin and effective status;
- one or more losing proposals, including origin, effective status, and
  `registration_key_conflict` reason.

Do not add acknowledgement, pagination, filtering, push delivery, or a general
problem-details framework. Conflict volume is expected to be very small in the
static environment.

`GET /registrations` must also return each contender as a separate registration
view. The winning proposal is `pending` or `accepted`. A losing proposal is
`rejected` with reason `registration_key_conflict`. The origin-specific existing
status route continues to return the proposal submitted on that machine.

## Work

1. Add documented public API models and the new operation in `platform/api`.
2. Add registration service queries that derive list and conflict views from
   retained contender records.
3. Register the exact conflict route before the parameterized status route and
   add HTTP method and error behavior tests.
4. Extend API conformance tests.
5. Regenerate `api-specifications/openapi.yaml`, its Markdown companion, and the
   .NET SDK. Do not edit generated SDK files by hand.
6. Add .NET SDK tests for reading winner and loser details.
7. Keep status fields as strings in this stage. The enum decision remains in
   `docs/backlog/api-contract.md`, and no new status value is required.

## Tests

- No conflict returns an empty JSON array.
- One unit key with two proposals returns one grouped conflict.
- Winner and loser ordering is deterministic.
- The losing origin's status lookup returns `rejected` with the conflict reason.
- The winning origin remains `accepted`.
- `GET /registrations` exposes both proposals.
- OpenAPI regeneration is deterministic and the generated SDK builds.
- `task fast` passes.

## Exit criteria

- Operators and clients can query all known registration conflicts.
- The endpoint distinguishes the surviving registration from rejected
  contenders without consulting event files.
- The generated contract and SDK match runtime behavior.

## Implementation result

Completed on 2026-07-15. `GET /registrations/conflicts` returns deterministic
resolved conflict groups containing the unit key, winner, and rejected losers.
The normal registrations list already exposes every contender as an individual
view. The route rejects non-GET methods with the standard `405` response. The
OpenAPI documents and Kiota client were regenerated; SDK coverage deserializes
winner and loser details from a conflict response. No acknowledgement,
pagination, filtering, or active notification behavior was added.
