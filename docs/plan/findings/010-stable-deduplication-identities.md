# F010: Transport deduplication is not correctness

Category: Consistency  
Severity: Critical  
Status: Resolved  
Source: Stages 2 and 3

## Finding

NATS deduplication has a finite window. A handler can publish the same logical
decision again after that window or after an uncertain acknowledgement.

## Resolution

Proposals, decisions, and acceptances have stable, event-kind-prefixed
identities. Projection reducers are idempotent by domain identity. NATS message
deduplication remains an optimization.

## Recommendation

Require every future handler-produced fact to define a stable domain identity.
Test redelivery after transport deduplication is no longer available.
