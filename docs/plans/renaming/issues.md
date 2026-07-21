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
