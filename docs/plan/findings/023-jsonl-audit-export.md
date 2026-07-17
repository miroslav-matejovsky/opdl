# F023: JSONL is not a second audit source

Category: Data ownership  
Severity: Medium  
Status: Resolved  
Source: Stages 1 and 5

## Finding

Keeping the old per-node JSONL sink would create two writable event histories,
with unclear authority and different ordering.

## Resolution

Stage 5 deleted JSONL and the optional recorder. NATS JetStream is the only
retained source used for coordination and replay.

## Recommendation

If operators need files, deploy a separate read-only audit projector with its
own checkpoint. Its failure must not affect journal publication, and its output
must identify the source journal sequence.
