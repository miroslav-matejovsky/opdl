# F015: Readiness belongs to runtime composition

Category: Lifecycle  
Severity: Critical  
Status: Resolved  
Source: Stages 2 and 4

## Finding

A connected transport cannot know that domain projections caught up or durable
handlers drained retained work. Adapter-level readiness would be false.

## Resolution

Runtime composition starts the ordered projector, catches up to a captured
high-water mark, starts and drains handlers, catches up again, publishes the
ready fact, waits for it locally, and only then serves HTTP. The sequence is
bounded by `catch_up_timeout`.

## Recommendation

Keep lifecycle ownership in `internal/app`. New stateful services must join this
gate before the node can serve them.
