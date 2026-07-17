# F005: Handler publications need causal links

Category: Observability  
Severity: Medium  
Status: Resolved  
Source: Stages 1 and 3

## Finding

Causation cannot be supplied by an event payload or plain recorder. It exists
only while a handler processes an input delivery.

## Resolution

`eventfabric.CausalContext` carries the input record ID into publication. The
adapter stamps causation and continues correlation across the workflow. Events
that begin a workflow correctly have no causation ID.

## Recommendation

Use causal context for every handler-produced event. Add trace export later by
projecting these links instead of adding transport headers to domain code.
