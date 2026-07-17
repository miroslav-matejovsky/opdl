# F013: The NATS cluster must contain only storage nodes

Category: Deployment  
Severity: Critical  
Status: Resolved  
Source: Stages 2 and 4

## Finding

With NATS 2.11.9, configured Core NATS routes increase the expected JetStream
metadata peer count even when those servers store no journal. A non-storage
server can therefore remove quorum without adding storage.

## Resolution

Only deterministic storage nodes run NATS servers. Other platform machines are
clients. Sites with fewer than three machines use one storage node and one
replica. Larger sites use three storage nodes and three replicas.

## Recommendation

Require at least three platform machines for deployments that must tolerate one
journal node failure. Re-test metadata formation before every NATS server major
or minor upgrade.
