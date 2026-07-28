# Step 02 — Codify and enforce the level dependency rule

| | |
| --- | --- |
| Complexity | Low |
| Effort | 0.5–1 person-day |
| Depends on | 01 |
| Blocks | — (guards every later step) |

## Goal

The dependency direction of the hierarchy is written down once and enforced
by the build, so a violation fails a test instead of waiting for a reviewer
to notice it.

## The rule

A higher level may import a lower level, never the reverse. The abstract
contract is importable by everyone and imports no level.

| Package | May import |
| --- | --- |
| `internal/site/...` | `internal/machine/...`, `internal/instance/...`, `internal/events/...` |
| `internal/machine/...` | `internal/instance/...`, `internal/events/...` |
| `internal/instance/...` | `internal/events/...` |
| `internal/events/...` | none of the above |
| `internal/app`, `internal/httpapi` | anything (composition and HTTP edge) |

Notes for reviewers, to be recorded with the rule:

- "May import" is permission, not encouragement. Site reaching into machine
  internals should stay rare and deliberate; the common shared surface is
  `internal/events`.
- `internal/app` is the composition root. It is where publishers, stores,
  and consumers of all three levels are wired together, so it is exempt.

## Actions

1. Write the rule into `docs/01-architecture.md` (extend the "Runtime
   boundaries" section) and into `AGENTS.md` so both humans and agents see it.
2. Enforce it with a plain Go test in the platform module, e.g.
   `platform/internal/archtest/deps_test.go`, using
   `golang.org/x/tools/go/packages` (already an indirect ecosystem
   dependency) or `go list -deps -json` to walk the import graph of
   `internal/...` and assert the table above. A test is preferred over a
   linter config because it needs no new tooling, runs in `go test ./...`,
   and its failure message can name the offending import chain.
3. Seed the test with the current graph and fix any violation found. After
   step 01 the expected state is clean; if a violation surfaces, resolve it
   in this step while it is still one import, not a pattern.

## Acceptance criteria

- The test fails when a synthetic reverse import is added locally (verify
  once, then revert), and passes on the real tree.
- The rule text in `docs/01-architecture.md` and the test agree on the exact
  allow-list, including the `app`/`httpapi` exemption.

## Risks / open questions

- If the team later adopts golangci-lint, the test can be replaced by a
  `depguard` config; the rule text is the durable artifact, the enforcement
  mechanism is fungible.
