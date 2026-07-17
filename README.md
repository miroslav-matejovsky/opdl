# Operational Platform Distribution Line (OPDL)

OPDL builds one deployable platform binary per machine. A project blueprint
describes the machines. The builder resolves and embeds each machine's deployment
descriptor, then packages the platform runtime. Machine identity is compiled in,
not configured at the deployment site.

## Documentation

Start with the [documentation index](docs/README.md).

- [Architecture](docs/01-architecture.md)
- [Registration](docs/02-registration.md)
- [Backlog](docs/backlog/README.md)
- [Bugs](docs/bugs/README.md)

## Repository

| Area | Purpose |
| --- | --- |
| `builder` | Build one platform package per blueprint machine. |
| `platform` | Run the registration API and the site event journal it projects from. |
| `conformance-tests` | Regenerate API artifacts and verify shared contracts. |
| `scenarios` | Run black-box tests against built binaries. |
| `sdk-dotnet` | Generated .NET client and end-to-end tests. |
| `api-specifications` | Generated OpenAPI YAML and Markdown. |

## Commands

- `task all` runs the complete required validation gate.
- `task fast` runs checks and unit tests.
- `task build -- customer-a` builds a project.
- `task plan -- customer-a` prints descriptor derivation.
- `task --list` shows all tasks.

Development conventions are in [AGENTS.md](AGENTS.md). Known unfinished work is
kept in [.todo](.todo).
