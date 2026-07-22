# Stage 06: per-package enable and disable selection

Effort: S (about 0.5 day). Complexity: Low-Med.

## Goal

Let the command enable or disable scenarios per package. All packages are
enabled by default. Flags narrow the run to a subset or exclude some packages.
This is the "parameter to enable/disable scenarios per package" the refactor
called for, and it belongs in the command, not the taskfile.

## Flag design

Add to `cmd/main.go` (on top of the flags from stage 04):

- `-only a,b,c`: run only the named packages. Empty means all.
- `-skip x,y`: run everything except the named packages.
- `-list`: print the known packages and their scenarios, then exit 0. Useful for
  discovery and for confirming names.

Rules:
- `-only` and `-skip` are mutually exclusive; giving both is an error.
- Names are the package names (`build`, `registration`, `nats`, `resilience`,
  `standby`, `sdk`), matching `runner.Set.Package`.
- An unknown name is an error, so a typo fails loudly instead of silently
  running everything.

`-run PATTERN` from stage 04 still works and composes: `-only nats -run
FourMachine` selects within a package. Package selection filters `runner.Set`s;
`-run` filters individual scenarios through the testing runner's matcher.

## Implementation

Filtering happens before the sets are flattened into `[]testing.InternalTest`:

```go
func Select(sets []runner.Set, only, skip []string) ([]runner.Set, error) {
    // validate names against the set list
    // return the filtered slice, error on unknown name or on both only and skip
}
```

Keep this in `internal/runner` next to the flatten function, so the command
stays declarative: parse flags, call `runner.Select`, call `runner.Run`.

The per-package default is enabled. There is no persisted config; selection is
per invocation. That matches the resilience-gate intent: the full set runs in
CI, and a developer narrows locally.

## Steps

1. Add `-only`, `-skip`, `-list` flags to the command.
2. Add `runner.Select` with validation and the mutual-exclusion rule.
3. Add `runner.List` (or inline in the command) for `-list`.
4. Document the flags in `cmd/doc.go` or a `-h` usage string.

## Files touched

- Edited: `scenarios/cmd/main.go`.
- Edited: `scenarios/internal/runner/runner.go` (add `Select`, `List`).

## Verification

- `go run ./cmd -list` prints six packages and their scenarios.
- `go run ./cmd -only nats -run x` selects only `nats`, matches
  nothing, exits 0.
- `go run ./cmd -skip sdk,standby -run x` excludes two packages.
- `go run ./cmd -only bogus` errors with a clear message and non-zero
  exit.
- `go run ./cmd -only nats -skip sdk` errors (mutually exclusive).
- `task fast` passes.

## Risks and notes

- Low risk; this is additive and localized.
- Keep the name set as the single source of truth (`runner.Set.Package`). Do not
  hardcode the package name list in two places; derive validation from the sets
  passed in.
- If a future need arises for finer control (individual scenarios on or off by
  name without regex), `-run` already covers it; do not build a second
  mechanism unless it is actually needed.
