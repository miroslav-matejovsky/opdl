# Event Fabric

Focused follow-up work for the retained NATS site journal.

Most of the original three-storage-node item is now proven by
`TestFourMachineStorageTopologyAndFailure`: three machines are selected by sorted
name, only those three bind a cluster listener, the fourth is client-only,
publication and replay cross machines, the site keeps accepting and projecting
after one storage machine stops, and the stopped machine rejoins its own storage
and reconverges. What remains unproven is recorded below.

## Prove a client-only machine survives losing its own storage node

**Effort:** medium. **Value:** high.

A machine that does not store the journal connects to the site's storage nodes as
a client. When the storage node it happens to be connected to is stopped, it must
reconnect to another and resume projecting from its last applied sequence.

The scenario shows this does not reliably happen within 60 seconds. With `node-c`
stopped, the surviving storage machines confirm a new proposal promptly while the
client-only `node-d` intermittently does not, and recovers only once the site is
whole again. It is intermittent because the NATS client randomises which storage
node it connects to, so a run only exercises the case when `node-d` was connected
to the machine that was killed.

The scenario therefore requires the surviving storage machines to keep accepting
and projecting, and requires every machine to reconverge after the rejoin. It
does not assert this property, because it does not hold.

Acceptance:

- the Event Fabric client logs the server address it is connected to and every
  reconnect, so the case is observable rather than inferred;
- the scenario pins the client-only machine to the storage node it will stop,
  making the failure deterministic instead of one run in three;
- a client-only machine resumes projecting within a stated bound after losing its
  server, and the scenario asserts it.

Evidence is in `docs/plan/issues.md`, issue 3.

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
