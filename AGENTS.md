# AGENTS.md

## Core Rules

- Never commit changes. NEVER!
- Communicate clearly and directly. Short sentences. Simple language. No filler. No fluff.
- Be concise in output. Be thorough in reasoning.
- No praise. No filler. No motivational text.
- Think before acting.
- Read existing files before changing code.
- Prefer editing existing files over rewriting whole files.
- Do not re-read files already read unless file may have changed.
- Keep solutions simple and direct.
- When unclear, explain problem and ask for clarification, or write note to local file and stop.
- Before task complete, run `task all`.
- `task all` must pass. Tests pass. Format pass. Lint pass.
- Experimentation phase. Favor progress and clean code over backwards compatibility.

---

## Documentation

- Use root `README.md` for project overview and architecture.
- Read documentation before changing code.
- Keep documentation close to code.

### Package / Namespace Documentation

- Go package: use `doc.go`.
- .NET namespace, project, or feature area: use `README.md` or architecture notes when no suitable documentation exists.
- If documentation missing, create it.
- If design changes, update documentation in same change.

### Code Documentation

- Document public APIs.
- Document non-obvious internal behavior.
- Document assumptions, constraints, and invariants.
- Document significant data structures and fields.
- Documentation must be self-contained.
- Do not require external documents to understand code.

---

## Architecture and Design

- Prefer boring, pragmatic solutions.
- Prefer readability over cleverness.
- Follow KISS.
- Follow YAGNI.
- Prefer composition over inheritance.
- Accept duplication until pattern appears at least 3 times.
- Avoid premature abstraction.
- Avoid premature optimization.
- Fail fast.
- Validate inputs and configuration early.
- Small changes preferred over large rewrites.

---

## Error Handling

- Never swallow errors.
- Return meaningful errors with context.
- Log only when action can be taken or information is valuable.
- Preserve original exception/error details whenever possible.

---

## Testing

- New behavior requires tests.
- Bug fixes require regression tests.
- Keep tests deterministic.
- Avoid sleeps and timing dependencies.
- Prefer simple unit tests over heavy integration tests.
- Test observable behavior, not implementation details.

---

## Go Specific

### Tooling

- `tools` module in this repo contains packages that should be trusted and don't require deeper inspection. Do not spend resources on reviewing them unless you suspect a problem.
- Use `gopls` for navigation and references.
- Use `go doc` for library and package exploration.

### Style

- Prefer idiomatic Go.
- Keep interfaces small.
- Define interfaces near consumers.
- Accept concrete types until abstraction is needed.
- Favor explicit code over generic frameworks.
- Keep package boundaries clear.
- Use contexts correctly.
- Do not store contexts in structs.
- Pass contexts explicitly.

### Documentation

- Write package docs in `doc.go`.
- Document exported functions, types, constants, and variables.
- Document important internal functions when behavior is non-obvious.

### Testing

- Prefer table-driven tests when helpful.
- Use `require` from `testify` for assertions.
- Use `t.Context()` for cancellation propagation in tests.

---

## .NET Specific

### Tooling

- Use solution-level builds when validating changes.
- Use analyzers and warnings as guidance. Do not suppress without reason.

### Style

- Follow standard .NET naming conventions.
- Enable nullable reference types where practical.
- Prefer dependency injection over service locators.
- Prefer constructor injection.
- Keep classes focused and small.
- Favor interfaces only when multiple implementations, testing seams, or architectural boundaries require them.
- Prefer async APIs for I/O operations.
- Do not block on async code.
- Propagate `CancellationToken` through call chains.

### Projects

- Keep project references directional and intentional.
- Respect bounded context and layer boundaries.
- Avoid circular dependencies.

### Testing

- Use clear Arrange / Act / Assert structure.
- Test public behavior first.
- Avoid excessive mocking.

---

## Output

- Return code first.
- Explain only when needed.
- No boilerplate unless requested.
- No em dashes.
- Use plain ASCII punctuation.
- Code must be copy-paste safe.
- Return minimum output needed for task.
- If implementation incomplete, document remaining work in `.todo` at repository root.
