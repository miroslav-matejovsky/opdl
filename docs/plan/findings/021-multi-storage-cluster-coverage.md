# F021: Three-storage cluster behavior needs a focused test

Category: Testing  
Severity: High  
Status: Open  
Source: Stages 2 and 4

## Finding

Single-storage restart and multi-machine runtime behavior are covered, but a
simultaneous three-storage-node JetStream cluster is not exercised as one focused
adapter integration test. That is the topology used for failure tolerance.

## Resolution

Topology derivation and replica counts have unit coverage. The corrected
storage-only rule was measured against NATS 2.11.9, but failure and rejoin of a
three-node metadata group remain a coverage gap.

## Recommendation

Add an isolated three-server test before calling the deployment production
ready. Verify publish and replay from different nodes, one-node loss, quorum
writes, restart, and rejoin. Keep generous deterministic readiness bounds.
