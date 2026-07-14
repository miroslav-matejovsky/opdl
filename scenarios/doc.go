// Package scenarios runs black-box, end-to-end checks of the distribution
// line: it drives the builder CLI as a subprocess to produce a deployment
// package, then runs the resulting platform binary as a subprocess and checks
// its behavior from outside the process, never through internal APIs.
//
// This is intentionally minimal today: one scenario builds the single machine
// in the "scenario" example blueprint and checks the binary runs. It has no
// dependency on the builder or platform Go modules; both are driven the same
// way a customer would, as external commands. More scenarios join as the
// platform grows an observable surface to check.
package scenarios
