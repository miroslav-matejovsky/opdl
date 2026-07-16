# Implementation Plans

This folder contains actionable, step-by-step technical plans for cross-cutting architectural changes or multi-step refactorings.

## Active Plans

- [Platform Configuration & Cleanup](platform-configuration-and-cleanup.md) — Moving configuration constants out of code into a strict, comment-documented TOML configuration file (`file.go` and `config.go`), eliminating code defaults, and replacing `platform/doc.go` with `platform/README.md`.
