# F020: Non-storage nodes depend on storage-node boot order

Category: Availability  
Severity: High  
Status: Mitigated by deployment  
Source: Stage 4

## Finding

A non-storage machine cannot reconstruct state or start serving until it can
connect to a storage node and find the site journal. In a one- or two-machine
site, one deterministic machine is the only storage node.

## Resolution

Startup fails clearly within `startup_timeout`; it never serves an empty local
projection. Three-machine sites use three storage nodes and tolerate one storage
node being unavailable.

## Recommendation

For resilient deployments, require at least three machines with stable storage
and start them before client-only nodes. Accept explicit storage-first boot order
for one- and two-machine POCs. Document the sorted machine selection in the
deployment runbook.
