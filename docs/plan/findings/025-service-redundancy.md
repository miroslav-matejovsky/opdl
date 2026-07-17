# F025: Journal replication is not service redundancy

Category: Availability  
Severity: Critical  
Status: Open  
Source: Architecture review during Stage 4

## Finding

There is one platform process per machine. Three journal replicas preserve event
history, but no standby takes over a machine's API or registration decisions.
There is no election, fencing, failover, or zero-downtime upgrade mechanism.

## Resolution

Not resolved by the event migration. Treating storage replication as service
redundancy would hide the risk.

## Recommendation

Before production, implement the planned local warm standby with explicit
single-writer fencing and tested failover. A minimum of three storage nodes
protects history but does not replace that feature. Until then, accept service
interruption and pending decisions during node loss.
