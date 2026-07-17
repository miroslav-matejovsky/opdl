# F012: Historical platform-instance IP may be empty

Category: API contract  
Severity: Low  
Status: Accepted  
Source: Stages 3 and 4

## Finding

Proposals retain expected machine names, not every expected IP. A later
descriptor may no longer contain a historical machine needed by a query view.

## Resolution

The API keeps the `ip` field and returns it empty when the answering node cannot
resolve that historical machine. Machine name remains the acceptance identity.

## Recommendation

Keep IP as display metadata. If immutable historical addresses become required,
add explicit topology facts rather than expanding registration identity for a
rendering field.
