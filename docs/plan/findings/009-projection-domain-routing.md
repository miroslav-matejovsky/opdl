# F009: Projection routing supports one stateful domain

Category: Architecture  
Severity: Medium  
Status: Mitigated  
Source: Stages 2, 3, and 4

## Finding

The node-wide registration projection receives the entire site journal. It
ignores events outside registration and stays strict for unknown registration
events. This is simple for one stateful domain but does not scale cleanly.

## Resolution

The lifecycle-event collision is fixed without a generic dispatcher. The
current runtime has only one stateful domain.

## Recommendation

Add a small domain dispatcher when the second stateful domain is introduced.
Fan one ordered delivery to matching domain projections and advance a shared
applied sequence only after every match succeeds. Do not build it earlier.
