# Platform operations guide

This guide is the operational contract for a packaged OPDL platform machine.
It covers deployment, monitoring, local operational events, and incident
response. The platform's primary stakeholder is the operations team, so every
runtime transition needed to explain availability is exposed without enabling a
NATS monitoring port.

Read in this order:

1. [Deployment](deployment.md) for topology, files, ports, service ordering, and
   security controls.
2. [Monitoring](monitoring.md) for status files, operational JSONL, event-derived
   metrics, and alert recommendations.
3. [Switchover and upgrade](upgrade.md) for moving active ownership on purpose
   and for rolling a machine's binary with bounded interruption.
4. [Troubleshooting](troubleshooting.md) for symptom-driven investigation and
   recovery procedures.

## Operational surfaces

| Surface | Purpose | Availability |
| --- | --- | --- |
| Process stderr | Complete structured operational event stream plus startup text | Always enabled; capture with the service manager |
| `operations.event_dir` JSONL | Append-only copy of structured events for local analysis and tests | Optional |
| Per-role status file | Current lifecycle, PID, projection sequence, lag, failover readiness, and last error | Written once startup reaches status composition, then every second |
| Public HTTP API | Service availability and registration behavior | Active instance only |
| Primary Ownership | Which local process owns active capabilities | A `Global\` named mutex; observe through `platform.fence_opened` and `fence_acquired`, not the filesystem |
| Site event journal | Durable platform and registration facts | Internal Event Fabric contract |

Operational events are local because they must describe loss of the Event
Fabric itself. Domain events remain durable facts in the site journal. Do not
replace one with the other.

There is no NATS HTTP monitoring listener. Do not open ports 8222 or 8223. The
supported signals above provide the platform view without exposing an
unauthenticated transport-specific surface.
