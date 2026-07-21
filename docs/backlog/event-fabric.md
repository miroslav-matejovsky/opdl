# Event Fabric

Focused follow-up work for the retained NATS site journal.

The original three-storage-node item is now proven by
`TestFourMachineStorageTopologyAndFailure`: three machines are selected by sorted
name, only those three bind a cluster listener, the fourth is client-only,
publication and replay cross machines, the site keeps accepting and projecting
after the client-only machine's selected storage server stops, and the stopped
machine rejoins its own storage and reconverges. Structured connection events
make the disconnect and reconnect explicit. The remaining product decision is
recorded below.

## Decide whether the platform absorbs the post-failure write window

**Effort:** small. **Value:** medium.

Immediately after a storage machine is lost, the journal's replica group elects a
new leader, and writes submitted during that window are rejected rather than
held. The platform does not retry internally, so the error reaches the client.

The scenario retries as a real client would, and says so. The open decision is
whether a bounded publish retry belongs in the platform. Absorbing the window
makes single writes more reliable but hides back-pressure; leaving it means every
client needs retry logic. Either is defensible; the contract should state which
one it is.

The four-machine failure scenario preserves this behavior explicitly in
`proposeEventually`; the harness fix does not change the production write
contract.

### Observed as intermittent scenario failure

`TestFourMachineStorageTopologyAndFailure` has been seen to fail under full-suite
load, and to pass on the next run and when run alone. The failing run's client-only
machine logged `event_fabric.projector_reset` and `handler_reset` after
reconnecting to a surviving storage server, then
`event_fabric.projector_attach_retry` and `handler_attach_retry` with
`context deadline exceeded`.

That is this item's window, reached through re-attachment rather than publication:
with eleven scenarios competing for CPU, re-attaching a consumer while the replica
group is still electing exceeds its deadline. It is evidence for deciding the
question above rather than a separate defect, and it is worth knowing before
chasing the scenario as flaky in its own right.
