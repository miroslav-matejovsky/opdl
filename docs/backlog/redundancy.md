# Local redundancy backlog

## Collect cross-platform failover percentiles before stating an SLO

Effort: Small. Value: Medium.

The 2026-07-18 local black-box baseline measured about 29.9 seconds from forced
primary death to standby activation, against about 287 ms for a planned handover.
That gap is gone, and its cause is identified.

The standby had been given its own NATS client address while the only running
server was the active process's, so it never reached the journal and was never
warm. On forced primary death the newly Active process had to complete a cold startup
bounded by the same 30 second Event Fabric startup timeout, which is why the
measurement sat within a rounding error of that bound. Sharing one machine-level
endpoint between the two processes removed it.

Three consecutive Windows runs of `standby.WarmStandbyFailoverAndPreferredPrimary`
after the change:

| Run | Catch-up | Failover | Listener unavailable | Failback |
| --- | ---: | ---: | ---: | ---: |
| 1 | 110.8 ms | 182.6 ms | 192.9 ms | 275.8 ms |
| 2 | 108.3 ms | 126.8 ms | 133.5 ms | 266.6 ms |
| 3 | 91.9 ms | 149.4 ms | 155.2 ms | 177.8 ms |

What remains is evidence collection, not investigation:

- repeated runs on more than one Windows host, not just a developer machine;
- percentiles rather than three samples;
- a stated SLO only once those exist.

Do not quote the numbers above as a failover SLO. They are three samples on one
host and establish only that the phase which dominated the old measurement is
gone. Background is in `docs/plan/issues.md`, issue 4.
