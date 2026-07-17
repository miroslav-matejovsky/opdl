# F003: Schema version applies to payloads

Category: Event contract  
Severity: Medium  
Status: Accepted  
Source: Stage 1

## Finding

`schema_version` identifies the payload schema for one event type. It is not a
single version for the common envelope.

## Resolution

Each event type evolves independently. Replay fails on an unsupported payload
version instead of guessing how to decode it.

## Recommendation

Keep payload versions. Add an envelope version only when the envelope itself
must change incompatibly. Do not overload the existing field.
