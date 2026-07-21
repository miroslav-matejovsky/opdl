# Stage 1: one global port pool

## Goal

Replace per-scenario ephemeral port reservation with a single process-wide
pool that hands out ports outside the operating system ephemeral range. The
suite stays serial. Nothing about scenario behavior changes.

This is the enabling change for stage 4 and, on its own, removes the largest
piece of accidental complexity in the harness.

## Why

Today each scenario reserves its own ports by listening on `:0`
(`scenarios/harness_test.go:153` and `harness_test.go:312`), then releases and
lets a platform process bind them later.

Two facts make that unsafe under concurrency:

1. Port `0` allocates from the ephemeral range. Windows uses 49152 to 65535,
   Linux uses 32768 to 60999. That range is shared with every outbound
   connection made by the harness, by NATS clients, and by `dotnet test`.
   A released port can be consumed by an outbound socket before the machine
   binds it.
2. Release-then-bind is a time-of-check gap. Serially there is only one
   scenario in that gap at a time. In parallel there are several, and they
   compete with each other as well as with the ephemeral allocator.

The current design fights fact 2 by holding listeners open through rendering
and building (`harness_test.go:136`). That is real complexity: a reservation
list, a cleanup safety net, and a `release` closure threaded out of
`stageBlueprint` into `deploySite` and called at exactly the right moment
(`harness_test.go:330`). It also does not help the API addresses, which are
reserved and released in three lines at `harness_test.go:312`.

Choosing ports the operating system will never hand out on its own removes
fact 1, and once fact 1 is gone there is nothing left for the held listeners
to defend against inside one process. The hold-and-release lifecycle can go.

## Design

Add a pool to `utils/testnet`. Do not change `Reserve` or `ReserveOn`.

```go
// utils/testnet/pool.go

// Take returns count ports on host that no other Take in this process has
// returned, and that nothing on the host is currently listening on.
func Take(host string, count int) ([]int, error)
```

Decisions:

- **Band: 20000 to 32767.** Below the Linux ephemeral start of 32768 and well
  below the Windows start of 49152, so the operating system never assigns
  these on its own. 12768 ports against a suite that needs roughly 40.
- **Never reuse inside a process.** A monotonic cursor over the band, never
  rewound. Two concurrent scenarios cannot be handed the same number, which
  is the whole property stage 4 depends on. There is no `Release`, so there
  is no lifecycle to get wrong.
- **Random start offset.** The cursor starts at a random point in the band.
  Two `go test` processes on one host therefore rarely overlap without any
  coordination between them.
- **Bind probe before handing out.** Listen on `host:port`, close it, return
  the number. This does not defend against anything inside the process, since
  the cursor already does that. It converts a foreign process holding the port
  into a clear allocation error instead of an obscure NATS bind failure later.
  Fail fast, at the place that knows what it was asking for.
- **One cursor for every host.** Ports are free per interface, so a strictly
  correct pool would track 127.0.0.1 and 127.0.0.2 separately. With 12768
  ports and 40 in use, tracking them together wastes nothing and removes a
  map. The probe still runs against the caller's host.
- **No per-process sub-banding.** A PID-derived sub-band would harden the
  cross-process case further. The random start plus the probe covers it, and
  YAGNI says do not build the rest until a cross-process collision is
  actually observed.

Give up after a full pass over the band and return an error naming the host,
the count, and how many probes failed.

## Changes

`utils/testnet/pool.go` (new)
- `Take`, the cursor, its mutex, and the band constants.
- Document why the band is what it is. The reasoning above is the point of the
  package and must not have to be rediscovered.

`utils/testnet/pool_test.go` (new)
- Distinct ports across many concurrent `Take` calls.
- Every returned port is inside the band.
- Returned ports are bindable.
- A port a foreign listener holds is skipped rather than returned.
- Count validation, matching the existing `Reserve` behavior.
- Exhaustion returns an error rather than looping.

`utils/testnet/doc.go`
- Describe both models: `Reserve` for a caller that wants the operating system
  to choose and will bind immediately, `Take` for a caller that must publish a
  port before binding it.

`scenarios/harness_test.go`
- `stageBlueprint` (`:136`) calls `testnet.Take(fixture.ip, 2)`. Drop the
  `reservations` slice, the cleanup safety net, and the `release` return
  value. The signature loses its third result.
- `deploySite` (`:302`) calls `testnet.Take("127.0.0.1", len(fixtures))` for
  the API addresses. Drop the reserve-then-release pair at `:312` and the
  `releasePorts()` call at `:330`.
- Delete `portOf` (`:189`). `Take` returns integers already.
- `natsPorts` (`:124`) holds numbers, and the `host:port` strings it currently
  carries are composed where they are used.

## Risks

- A developer service in the 20000 to 32767 band. The probe skips it, so the
  cost is a skipped number, not a failure.
- Something outside this repo assumes scenario ports are ephemeral. Nothing
  does. The band is an internal detail of the harness.
- Reduced defense against a foreign process, since listeners are no longer
  held from allocation to bind. This is the deliberate trade. Inside the
  process the cursor is a stronger guarantee than the listeners ever were, and
  outside it the old design was already only holding listeners for part of the
  window.

## Verification

- `task test` covers the new pool in the fast gate.
- `task scenarios` passes unchanged, still serial.
- Run the scenario package twice concurrently from two shells. This exercises
  the cross-process path that the random start is there for, and it is the
  cheapest available preview of stage 4. Once stage 2 lands, the second shell
  must set `OPDL_SCENARIO_TMP` to a different root, because the scratch
  directory is a fixed path by then and the two runs would otherwise empty
  each other's.
- Confirm no scenario port lands in the ephemeral range:
  `netstat` during a run, or assert the band in the pool test and trust it.

## Done when

- `stageBlueprint` returns two values and holds no listeners.
- No scenario code calls `testnet.Reserve` or `testnet.ReserveOn`.
- `platform` tests still use `Reserve` and still pass.
- `utils/testnet` tests cover the pool and run under `-short`.
</content>
