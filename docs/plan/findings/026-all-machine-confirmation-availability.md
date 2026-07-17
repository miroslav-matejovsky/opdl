# F026: All-machine confirmation blocks while any machine is offline

Category: Availability  
Severity: High  
Status: Accepted for POC  
Source: Stages 1, 3, and 4

## Finding

Registration requires confirmation from every machine captured by the proposal.
Journal quorum does not change this domain rule. One offline expected machine
keeps new proposals pending indefinitely.

## Resolution

The projection exposes per-machine pending status and does not invent timeout,
quorum, or failover decisions. This is consistent and replayable.

## Recommendation

Require all expected machines to be operational when registration progress is
an availability objective. After service redundancy exists, let the fenced
active instance state the machine's decision. Consider quorum only as an explicit
domain-policy change with new event contracts, never as a deployment shortcut.
