# Event Fabric

Focused follow-up work for the retained NATS site journal.

## Prove the three-storage-node failure topology

**Effort:** medium. **Value:** high.

Add one focused integration test that starts the three storage nodes used by a
site of three or more machines. Verify publication and replay through different
nodes, continued writes after one node stops, and correct restart and rejoin.

The current suite proves single-storage restart and two-machine behavior. It does
not yet prove the one-node failure tolerance claimed by the three-replica
topology. Complete this before relying on that topology in production.
