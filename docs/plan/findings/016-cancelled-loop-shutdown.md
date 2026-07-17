# F016: Cancelled loops looked like failures

Category: Lifecycle  
Severity: Medium  
Status: Resolved  
Source: Stage 4

## Finding

Projector and handler loops once wrapped `context.Canceled` as a processing
failure when shutdown interrupted an in-flight delivery.

## Resolution

The adapter treats an error raised under a cancelled loop context as a normal
stop. Real processing errors still fail the loop and stop serving.

## Recommendation

Keep cancellation tests around every long-running loop. Never classify a
cancelled operation without checking the owning context.
