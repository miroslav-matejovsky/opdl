# F018: Journal capacity must fail writes visibly

Category: Operations  
Severity: High  
Status: Resolved  
Source: Stages 4 and 5

## Finding

The journal uses `DiscardNew` because deleting old history would make a complete
projection rebuild impossible. The configured 1 GiB limit was not previously
exercised.

## Resolution

A focused real-NATS adapter test now opens a small journal, fills it, and verifies
that publication fails instead of evicting replay history. Publication errors
retain route and operation context.

## Recommendation

Monitor journal bytes and alert before exhaustion. Capacity increases must be
planned before the limit is reached. Snapshots alone do not authorize history
deletion until restoration from snapshot plus retained tail is implemented and
tested.
