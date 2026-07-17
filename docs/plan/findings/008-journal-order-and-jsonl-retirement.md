# F008: Journal order replaces local sequence and JSONL order

Category: Data ownership  
Severity: Critical  
Status: Resolved  
Source: Stages 1, 2, 4, and 5

## Finding

The old envelope sequence and per-node JSONL append order could not provide one
site-wide order. Keeping JSONL after cutover would also create a second event
source with redundant node identity.

## Resolution

JetStream stream sequence is the only ordering authority. It lives on receipts
and deliveries, not immutable event records. Stage 5 deleted the recorder,
no-op recorder, JSONL sink, and their tests.

## Recommendation

Do not add another writable event store. Build any audit export as a read-only
subscriber whose loss does not affect replay or coordination.
