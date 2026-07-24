# Step 01 — Explicit serving mode on the HTTP boundary

Complexity: **Low** · Effort: **S (~½ day)** · Depends on: nothing

## Why

A Passive instance already keeps its listener bound and serves the health
endpoints and `/instance` while refusing everything else — the behavior is right.
What is wrong is that the behavior is *implied* by which constructor the runtime
happened to call (`httpapi.NewHandler` vs `NewPassiveHandler` vs
`NewJournallessHandler`), not stated anywhere a reader or a test can point at.
The requirement is explicit: Passive serves health checks and nothing else, and
accepts no writes. Make the boundary say so.

## Design

Introduce an explicit mode on `platform/internal/httpapi`:

```go
// ServingMode states what surface this instance's handler serves.
type ServingMode string

const (
    // ModeActive serves the whole API: health, identity, and domain operations.
    ModeActive ServingMode = "active"
    // ModePassive serves health and identity only. Every domain operation is
    // refused with a 503 naming the instance that owns, and no write is accepted.
    ModePassive ServingMode = "passive"
)
```

- The passive/refusing handler is built from `ModePassive` explicitly; the doc
  comment on the mode — not on a constructor — is where "health-only, read-only"
  is stated once.
- The refusal path already covers every registered domain route via
  `api.DomainPaths`; add a guard (belt-and-suspenders) that in `ModePassive` any
  non-GET/HEAD request is refused even if a future route is forgotten from
  `DomainPaths`. That is the "no writes" rule enforced structurally rather than
  by list maintenance.
- `NewJournallessHandler` stays as-is behaviorally (it is Active with no
  journal, a different reason to refuse), but its construction goes through the
  same mode-aware core so there is one place that decides what a mode serves.

No API-contract change: the OpenAPI spec, paths, and response shapes are
untouched.

## Tasks

1. Add `ServingMode` to `httpapi`; thread it through `newRefusingHandler` and the
   public constructors.
2. Add the non-GET/HEAD write guard for `ModePassive`.
3. Tests in `httpapi_test.go`: a table over every method × path proving Passive
   serves exactly `GET /health*` and `GET /instance` and refuses all else with
   503 (404 for unknown paths stays 404); a POST to an unregistered future-style
   path is refused, not 404.
4. Update `httpapi` doc comments so the mode is the single statement of the
   Passive contract.

## Done when

- `httpapi` compiles with the explicit mode and the runtime passes it.
- The method×path table test pins the Passive surface.
- `task deadcode`, arch-lint, and all platform tests stay green.
