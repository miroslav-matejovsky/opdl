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

Evidence is in `docs/plan/issues.md`, issue 2.
