# Stage 2: annotate contract types

## Goal

Teach the `platform/api` DTOs to describe themselves to huma, using struct tags
only, so the generated spec keeps the field docs, examples, integer bounds, and
closed value sets the current hand-rolled generator produces (and adds the ones
it cannot). `platform/api` stays dependency-free.

## Why tags, not imports

huma derives its JSON schema from struct tags: `json`, `doc`, `example`,
`minimum`, `maximum`, `enum`, `format`, and required-ness (a non-pointer field
without `omitempty` is required). Tags are plain strings, so the contract package
carries them without importing huma. This preserves the property stated in
`platform/api/doc.go`: one exported, dependency-free source that SDKs and the
conformance module build against.

## Changes

All in `platform/api/api.go`.

### 1. Field docs and examples

The user requirement is that examples live in the Go code. Add `doc` and
`example` tags to each field. Example for `RegistrationRequest`:

```go
type RegistrationRequest struct {
	UnitType uint8 `json:"unit_type" doc:"Unit type identifier, 0-255." example:"7"`
	UnitID   uint16 `json:"unit_id" doc:"Unit identifier, 0-65535." example:"42"`
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised" doc:"Advertised unit type name." example:"pump"`
	Role *string `json:"role,omitempty" doc:"Optional advertised role." enum:"Master,Slave" example:"Master"`
}
```

Do the same for `ProposalAccepted`, `Registration`, `RegistrationConflict`,
`PlatformInstanceRegistrationStatus`, and `Error` (if the `{code}` error model is
kept, see stage 3).

### 2. Integer bounds

The current generator emits `minimum: 0`/`maximum: 255` for `uint8` and
`0`/`65535` for `uint16` (see `integerSchema` in the old generator). Verify what
huma emits for unsigned types in v2.39.0. If huma does not already produce those
exact bounds, add them explicitly:

```go
UnitType uint8  `json:"unit_type" minimum:"0" maximum:"255" ...`
UnitID   uint16 `json:"unit_id" minimum:"0" maximum:"65535" ...`
```

This keeps the SDK's numeric validation identical to today.

### 3. Closed value sets (enums)

This closes the deferred item in `docs/backlog/api-contract.md`. `status` and
`role` are closed sets described today as free strings.

Two options:

- **Low friction (recommended for the POC):** add an `enum` tag on each string
  field that is closed:
  - `role`: `enum:"Master,Slave"`
  - `status` (on `Registration` and `PlatformInstanceRegistrationStatus`):
    `enum:"pending,accepted,rejected"`
  - `resolution_status` (on `RegistrationConflict`): `enum:"resolved"`

- **Stronger:** introduce named types (`type RegistrationStatus string`,
  `type Role string`) carrying their constants, and give them an `enum` tag or a
  `huma.SchemaProvider` implementation. This types the fields in Go too, not just
  in the spec. `SchemaProvider` would require importing huma into `api`; if that
  is unwanted, stay with the string-plus-`enum-tag` form.

Note the caution already recorded in the backlog: Kiota turns enums into C#
enums that reject unknown values, so adding a status or role later becomes a
breaking change for deployed clients. Both sets look stable; proceed for the POC.

### 4. Info metadata

Title, version, and description currently come from `Describe()`
(`"OPDL Platform API"`, `"0.1.0"`, `"HTTP API served by the OPDL platform
runtime."`). These move to the huma config in stage 3/4. Record them here so they
are not lost when `Describe()` is deleted.

## Verify

- `platform/api` still imports nothing new (`go list -deps` shows no huma).
- `task vet && task fmt && task test` pass. Existing `api` tests still hold; add
  a small test asserting a couple of tags exist if useful, though the real
  assertion is the generated spec diff in stage 4.

## Out of scope

- Removing `Describe()` and the structural `Contract`/`Operation` types. They are
  still used by the current generator until stage 4. Deleting them is stage 6.

## Exit criteria

Every DTO field carries the doc, example, bounds, and enum tags huma needs to
regenerate a spec at least as descriptive as today's, with no new import in
`platform/api`.
