// Package build is the builder's public build entry point.
//
// It runs the whole build flow behind one function so callers other than the
// CLI can produce deployment packages in-process: load and validate the
// blueprint, resolve the per-machine plan, and compile a package per machine.
// The builder CLI (cmd) is a thin wrapper over Run, and the scenarios harness
// calls Run directly instead of driving the CLI as a subprocess.
package build
