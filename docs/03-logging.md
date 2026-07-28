# Logging

Every instance writes a structured application log with `log/slog`. It is the
diagnostic account of what a process did. It is complementary to
[Events](02-events.md) and replaces neither.

## Logs and events

| | Application log | Event |
| --- | --- | --- |
| States | what a process was doing | a fact a package owns |
| Read by | a person, or a log tool | the platform, scenarios, and tooling |
| Contract | none; wording may change | typed payload, scope, and schema version |
| Destination | the instance's `log_file` | the instance event log, plus wider levels by scope |
| Loss | acceptable | a failure to report |

A log record is never routed by scope, consumed, or asserted on by the platform.
An event is never a diagnostic message. Adding a message nobody may act on to
the event flow would put it in a record everyone reads, which is why the two are
separate files.

## Configuration

Each deployed instance authors `log_file` in its blueprint block, beside
`eventlog_file` and `state_file`:

```hcl
primary {
  eventlog_file = "D:/opdl/customer-a/north/sensor/primary/events.jsonl"
  state_file    = "D:/opdl/customer-a/north/sensor/primary/state.json"
  log_file      = "D:/opdl/customer-a/north/sensor/primary/platform.log"
  ...
}
```

It is required on the primary, required on a deployed standby, and rejected on a
disabled one. The builder resolves it onto that instance's descriptor record as
`log_file` and rejects a path shared with any other file the machine owns. There
is no default: an unauthored path would resolve as the empty string, which is
not a file the runtime could open.

## What is written

One JSON object per line, at `INFO` and above. Every record carries the machine
and the instance role, stamped once when the process opens its log, so a line
lifted out of the file still says who wrote it:

```json
{"time":"2026-07-28T09:12:44.117Z","level":"INFO","msg":"listening","machine":"node-a","instance":"primary","address":"127.0.0.1:8080","instance_state":"passive"}
```

Records also go to the process error stream. A packaged instance runs as a
Windows Service and nothing reads that stream, but a developer running the
binary and the scenario harness capturing a failed run both do, and neither has
the log file to hand.

The startup configuration block the process prints to standard output is not a
log record. It is a rendering of the descriptor for a person watching a process
start; the same startup appears in the log file as one `platform starting`
record with the fields it was rendered from.

## Composition

`internal/instance/applog` opens the file and returns the logger.
`internal/app` is the only package that imports it: it opens the log first, so
every later failure to open something has somewhere to be described, stamps the
process identity on it, and closes it last.

Packages below the composition root do not take a logger. They state facts
through the publisher they were given, and where a diagnostic message goes is
the composition root's decision, so `app` installs the logger as `slog`'s
default and those packages log through the package-level functions.
`events.BestEffort` reports a publication failure that way: the pipeline that
could not carry the event is the same pipeline a diagnostic about it would use,
so the application log is the one destination left.

## Limits

There is no rotation, no retention, and no configurable level. The file grows
until something outside the platform truncates or moves it. Rotation is
deliberately absent rather than pending: a rotating writer is a second answer to
"where is this record", and it is not worth having before an operator has asked
for one.
