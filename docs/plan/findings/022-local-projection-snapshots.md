# F022: Local projection snapshots are deferred

Category: Performance  
Severity: Medium  
Status: Risk accepted  
Source: Stages 4 and 5

## Finding

Every restart replays the complete retained journal into memory. Startup time
and journal growth can eventually exceed operational objectives.

## Resolution

No snapshot was added during migration. Current data volume has not shown a
need, and premature snapshots would add restoration and retention complexity.

## Recommendation

Measure replay count, bytes, and duration. Add node-local snapshots only after a
startup objective is defined and exceeded. A snapshot is a cache, never shared
state. It must include the applied journal sequence and be validated by replaying
the retained tail.
