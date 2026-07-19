# Utility extraction plan

This folder plans a future standalone Go module at
`github.com/miroslav-matejovsky/opdl/utils`. Each candidate package has its own
plan so it can be extracted, reviewed, and validated independently.

Only policy-free code belongs in this module. A package must hide non-trivial
implementation detail, have more than one credible consumer, or isolate
operating-system behavior that callers should be able to trust without reading.
The module must not import `platform`, `builder`, `conformance-tests`, or
`scenarios`.

## Candidate packages

| Package | Priority | Functionality |
| --- | --- | --- |
| [`atomicfile`](atomicfile.md) | High | Atomically replaces complete files across supported operating systems. |
| [`filelock`](filelock.md) | High | Provides exclusive, crash-released, context-aware local file locks. |
| [`stablehash`](stablehash.md) | High | Hashes ordered strings with unambiguous length-prefix framing. |
| [`processtree`](processtree.md) | High | Starts, gracefully stops, or force-kills a child process tree. |
| [`testnet`](testnet.md) | Medium | Reserves distinct ephemeral loopback addresses for tests. |
| [`processinfo`](processinfo.md) | Low | Reads portable process metrics such as resident memory. |
| [`jsonfields`](jsonfields.md) | Low | Describes the JSON-visible fields of Go structs consistently. |

Priority reflects current cognitive load and risk, not implementation order.
Create the `utils` module and its package documentation as part of the first
extraction. Add it to `go.work`, then add explicit module requirements only to
consumers that use it.

## Code that stays where it is

The review covered Go code in `platform`, `builder`, `conformance-tests`, and
`scenarios`. .NET source, generated .NET code, and .NET-specific generation were
not considered.

The following code is intentionally excluded:

- Deployment descriptors, blueprint validation, package manifests, and builder
  orchestration encode OPDL distribution rules.
- Event routes, event envelopes, registration IDs, storage-node selection,
  redundancy state transitions, status schemas, and runtime path construction
  encode OPDL contracts. Only their policy-free primitives are candidates.
- OpenAPI generation and Markdown rendering describe this repository's API.
- Scenario machine, site, API, marker, and diagnostic helpers encode the OPDL
  black-box test protocol.
- Small wrappers around `os.ReadFile`, `json.Marshal`, sorting, duplicate checks,
  and command execution do not hide enough complexity to justify a package.
- File checksums currently have one narrow builder consumer and are already a
  direct standard-library operation.

## Shared completion gate

Every extracted package must have a self-contained `doc.go`, documentation for
all exported APIs, deterministic unit tests, and platform-specific tests where
behavior differs by operating system. Consumer tests must remain in place as
contract tests. Run `task all` after every extraction and require it to pass.
