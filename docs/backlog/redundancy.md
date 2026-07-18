# Local redundancy backlog

## Investigate the Windows forced-kill promotion gap

Effort: Medium. Value: High.

The 2026-07-18 local black-box baseline measured about 29.9 seconds from forced
primary death to standby activation, close to the configured 30-second Event
Fabric startup bound. Planned handover completed in about 287 ms.

Instrument standby client shutdown, fence acquisition, embedded NATS restart,
JetStream recovery, catch-up, and listener bind separately on Windows and Linux.
Do not declare a failover SLO until repeated CI measurements identify the slow
phase and provide percentiles.
