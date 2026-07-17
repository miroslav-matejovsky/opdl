# F019: Live projection lag is not exposed

Category: Operations  
Severity: High  
Status: Open  
Source: Stage 4

## Finding

Startup readiness is strict, and a failed projector stops HTTP serving. A slow
but still running projector can answer from stale local state because the HTTP
API exposes no current high-water, applied sequence, handler pending count, or
lag threshold.

## Resolution

Not resolved. The required data already exists in Event Fabric and projection
state.

## Recommendation

Add a small readiness endpoint before production use. Mark the node unready when
sequence lag or lag duration exceeds a configured bound. Include journal health,
applied and high-water sequences, handler pending counts, and last loop error.
