# Conformance Tests

Verifies that the independent representations of a shared contract stay
compatible across the workspace's modules, by regenerating the specification
artifacts from the code that owns them, always — never merely checking them for
staleness. Run it and the checked-in artifacts are exactly what the current source
produces; `git diff` shows whether anything drifted.

## Layout

The conformance logic is ordinary package code, not test code — each check package
could be a `main` on its own. Test files exist only where they earn their keep:

- `api-specifications/` — regenerates the platform's API specification. The
  OpenAPI specification (`api-specifications/openapi.yaml`), its compact Markdown
  companion for review (`api-specifications/openapi.md`), and the .NET client
  (`sdk-dotnet`) generated from the OpenAPI specification with Kiota. `Run`
  generates the specification (and its Markdown companion) first, then the client,
  since the client depends on the specification.
- `deployment-descriptors/` — verifies that the builder's and the platform's
  deployment descriptors describe the same contract, even though they are
  separate Go types in separate modules (the platform must not depend on the
  build tool) with nothing at compile time to keep them in sync. Unlike
  `api-specifications`, this has nothing to regenerate — a divergence here is a
  Go type that needs editing, not a stale artifact — so this check simply fails.

  Each package also has small unit tests of its own internal check logic
  (e.g. `TestSchemaForStruct`, `TestSignature`), independent of the platform API
  or any generated artifact.

- `cmd/` — the command that runs the checks above. It is both a runnable program
  and, via `main_test.go`, the entry point the workspace's test suite drives:

  ```
  go run ./conformance-tests/cmd    # regenerate the API specification artifacts and verify the descriptors
  go test ./conformance-tests/cmd   # the same, via the go test runner
  ```

This module ships no runtime code that another module imports; `cmd` is its only
command.
