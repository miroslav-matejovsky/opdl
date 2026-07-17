# F024: Architecture regression guards

Category: Maintainability  
Severity: High  
Status: Resolved  
Source: Stage 5

## Finding

Documentation alone cannot prevent a future domain from importing NATS directly
or reintroducing the removed Olric dependency.

## Resolution

`go-arch-lint` denies vendor dependencies by default and deep-scans method calls
and dependency injection. TOML is allowed only in configuration. NATS server and
client packages are allowed only in `internal/eventfabric/nats`. Runtime
composition may depend on that adapter, while domain packages may depend only on
the transport-neutral Event Fabric contract. The deleted state-fabric component
name remains reserved and no component may depend on it.

## Recommendation

Keep `task arch` in the required validation gate. Add vendors through explicit
`vendors` and `canUse` entries. Do not enable blanket vendor access and do not
ban maps because node-local in-memory projections are valid.
