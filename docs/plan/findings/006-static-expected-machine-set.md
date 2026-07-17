# F006: Registration captures a static expected-machine set

Category: Domain policy  
Severity: High  
Status: Accepted for POC  
Source: Stages 1 and 4

## Finding

A proposal captures every platform machine from the origin descriptor. The set
is part of proposal identity and does not change with current reachability or a
later descriptor.

## Resolution

This preserves deterministic replay and matches the current all-machine
acceptance rule. It also means topology changes require a new proposal.

## Recommendation

Keep this rule for the POC. Before adding service-specific deployments, define
which service role owns a decision and capture that role's resolved machine set
in the proposal. Never derive historical acceptance from live membership.
