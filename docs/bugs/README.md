# Bugs

Observed defects awaiting a fix. One bug per file. These are confirmed wrong behaviors
seen in real captures, distinct from the optional enhancements in
[`../backlog/`](../backlog/README.md).

Each file is self-contained and follows a loose shape:

- **Status / Severity / Area** - one line each.
- **Symptom** - what was observed, with the capture path and log/issue excerpts that
  prove it.
- **Root cause** - the code path at fault, cited by `file` and function.
- **Impact** - who or what it breaks, and when.
- **Fix direction** - the intended remedy, not a committed design.

Remove a file when its bug is fixed and the fix is confirmed by a fresh capture.
