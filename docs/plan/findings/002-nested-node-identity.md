# F002: Nested node identity

Category: Event contract  
Severity: Low  
Status: Accepted  
Source: Stage 1

## Finding

Event identity fields are represented by one nested `node` object instead of
five top-level envelope fields.

## Resolution

The nested shape is the wire contract. It keeps deployment identity cohesive
and every journal record remains self-contained.

## Recommendation

Keep the shape. Version any incompatible change as an event contract change and
update external journal consumers before deploying it.
