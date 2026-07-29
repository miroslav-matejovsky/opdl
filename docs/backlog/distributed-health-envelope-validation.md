# Backlog: distributed-host health envelope validation

**Priority**: P2

**Effort**: Medium

**Value**: Medium

## Gap

The accepted envelope is tested with 8 machines, 16 services per machine, two
platform instances per machine, and a 1 second probe interval. The current load
test runs the complete topology in one process against one embedded broker.
That is strong coverage for heap use, scheduling, fan-out accounting, snapshots,
and shutdown, but it does not measure the network and route behavior of eight
physical Windows hosts.

At the envelope, 256 observations are published each second. With sender echo
disabled, each observation is delivered over NATS to 15 peer connections and
applied directly at its sender. At about 700 bytes per observation this is
approximately:

- 180 KB/s of observation payload published into the cluster; and
- 2.7 MB/s of aggregate peer-delivery payload across all receivers, before NATS
  and network framing.

## Recommendation

Run the accepted envelope in a representative multi-host Windows environment.
Use the same production route topology, network policy, and service intervals.
Record results rather than adding machine-specific thresholds to unit tests.

## Measurements

- CPU and working set per platform instance.
- Aggregate and per-host route traffic.
- Publisher failures and supersessions.
- Subscriber rejections and view drops.
- Time to first complete view after startup.
- Snapshot and `GET /health/services` latency at 128 units.
- Shutdown duration with probes in flight.
- Convergence time after one broker or route reconnects.

## Acceptance

- No unexplained observation loss.
- No unbounded memory or goroutine growth.
- API latency and shutdown remain inside documented operational budgets.
- All instances converge within the documented bound after stable connectivity.
- The supported-envelope section in `docs/04-service-health.md` is updated with
  the host, network, and measured results.
