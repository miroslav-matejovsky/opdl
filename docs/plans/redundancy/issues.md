# Issues found while executing the renaming plan

Things noticed during the stages that are not the stage's own work. Kept here so a
stage stays scoped and nothing is quietly dropped.

## 1. The resilience gate is off

**Found:** stage 02 setup. **Severity:** medium. **Owner:** whoever re-enables it.

`Taskfile.yml` has `# - task scenarios` commented out, so `task all` runs neither
the black-box scenarios nor the platform integration tests. Both live in
`taskfile/scenarios.ps1`.

`task all` passing is therefore a weaker signal than it was for stage 01: it
covers formatting, vet, lint, deadcode, arch, unit tests with `-short`, the .NET
SDK build, and blueprint validation, and nothing that starts a real process or
binds a socket.

Consequence for these stages: renames are still compiler and unit checked, but
nothing proves a built package still starts, that ownership still transfers, or
that the manifest a machine ships is the one the runtime reads. Re-run
`task scenarios` before treating the plan as finished.

## 2. Blind sed on prose produces plausible nonsense

**Found:** stage 03. **Severity:** medium for the stages still to come.

Renaming `fence` to `ownership` with a word-boundary sed compiled cleanly and
produced text like "the exclusive machine ownership", "stealing the ownership from
a live standby", "not a ownership token", and "machine ownership ownership object
opened". All of it reads as English at a glance and none of it means anything.

The compiler cannot catch this, and neither can `task all`, because comments and
log messages are not typed. The damage was found only by grepping the replaced
word and reading every hit.

For stages 05 and 06, which rename `promotion`, `reclamation`, and the event
names: sed the identifiers, then replace prose with explicit before-and-after
pairs, then grep the new word and read every line. Budget for the reading.

## 3. Four-machine scenario is flaky under full-suite load

**Found:** stage 01. **Severity:** low, already tracked.

`TestFourMachineStorageTopologyAndFailure` failed once under full-suite load and
passed alone and on the next full run. The signature is
`event_fabric.projector_attach_retry` and `handler_attach_retry` with
`context deadline exceeded`, immediately after the scenario's deliberate storage
kill.

Recorded in `docs/backlog/event-fabric.md` as evidence for the open decision
there, rather than as a separate defect. Not caused by this plan.

## 4. The runtime is behind the descriptor contract

**Found:** per-instance endpoints work. **Severity:** high until closed.
**Owner:** whoever implements the two-server runtime.

The descriptor now resolves a NATS topology per instance, because each instance is
meant to run its own server and cluster with the other. The runtime does not do
that. `nats.DefaultConfig` reads the **Primary Instance's** topology whichever
instance is running, and the composer still turns a standby client-only so it
follows the address the Active instance is serving.

That is deliberate and commented at `platform/internal/eventfabric/nats/config.go`.
Reading the standby's own topology today would point it at an address nothing is
listening on, which is the defect the single-endpoint shape was written to
prevent. The contract is ready; the runtime is not.

Two things must land together: starting the standby's server, and taking the
per-instance topology. Doing either alone reintroduces the defect.

## 5. JetStream replica placement is unconstrained

**Found:** per-instance endpoints work. **Severity:** high, latent.

Storage is selected by machine, because a machine is the failure domain. But every
instance a storage machine deploys runs a server, so a site can now have more
storage servers than storage machines — six servers on three machines when every
machine deploys both instances.

`Replicas` is still computed from the machine count, which is right. What is
missing is placement: nothing stops JetStream putting two replicas of an R=3
stream on one host, and losing that host would then lose two of three replicas and
the stream with them. NATS solves this with unique-tag placement.

This cannot happen until issue 4 lands, since only one server per machine runs
today. It must be solved as part of that work, not after it.

## 6. The API address has two sources of truth

**Found:** per-instance endpoints work. **Severity:** medium.

The blueprint now authors each instance's `api.port`, and the builder derives
`instances.<role>.api_address` from it and the machine ip. The platform still
binds `address` from its TOML configuration file and never reads the descriptor's.

Both exist and they can disagree. The repo already rejects this shape for the NATS
sockets — `file.go` refuses a configuration that sets them, precisely because a
silently dropped setting looks exactly like an applied one.

It was left out of this change because closing it is a behavioral change, not a
rename: the TOML address is typically `127.0.0.1:8080`, binding loopback only,
while the derived address uses the machine's routable ip. Moving to the descriptor
would expose the API on the network. That deserves its own decision.
