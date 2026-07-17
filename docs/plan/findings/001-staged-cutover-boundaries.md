# F001: Staged cutover boundaries

Category: Architecture  
Severity: Medium  
Status: Resolved  
Source: Stages 1, 2, 3, and 4

## Finding

The event catalog, Event Fabric, registration services, and runtime composition
could not be replaced independently while the repository still had to compile.
Stage wording initially assigned some contracts to more than one stage.

## Resolution

The event model was isolated until its Event Fabric dependency existed. Stage 4
then promoted it and changed runtime composition, HTTP, generated clients, and
scenarios as one breaking cutover. No dual write or state import was added.

## Recommendation

Treat future source-of-truth changes as an atomic runtime boundary. Define the
contract first, build the adapter and domain in isolation, then perform one
cutover that deletes the old path.
