# F004: Event and route namespaces differ

Category: Event contract  
Severity: Low  
Status: Accepted  
Source: Stage 1

## Finding

Product event types use `platform.<domain>.<fact>`. Transport routes use
`opdl.<site-scope>.event.<domain>.<fact>`.

## Resolution

The distinction is intentional. Event type is a stable product contract. Route
adds transport and site isolation without changing the fact's identity.

## Recommendation

Keep route construction inside Event Fabric. Domain code must never construct
or parse NATS subjects.
