# F011: Invalid history and invalid proposals differ

Category: Consistency  
Severity: High  
Status: Accepted  
Source: Stage 3

## Finding

Skipping a forged identity, impossible event order, unsupported schema, or
contradictory decision would let a node serve a projection with a hole. A
structurally valid proposal can still fail current business validation.

## Resolution

Invalid journal history stops replay and keeps the node unready. A structurally
valid but semantically invalid proposal is projected and receives a deterministic
`registration_invalid_proposal` decision.

## Recommendation

Keep the fail-fast split. Add operator tooling to identify the failing sequence
before adding any repair workflow. Never silently skip retained facts.
