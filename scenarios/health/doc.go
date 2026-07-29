// Package health is the scenario category for distributed service health: two
// machines, three platform instances, and services the scenario decides the
// answer for.
//
// The claim being tested is convergence, so every assertion is made against
// every running instance at once. Asking one instance proves nothing about a
// site that has stopped agreeing with itself, and the endpoint exists precisely
// so an operator can ask either one.
//
// The services are the harness's own, bound at the address each machine's
// blueprint authored. Nothing about the probe is simulated: the platform reaches
// them through the URL its descriptor composed, on the machine's own ip. What
// the scenario controls is only what they answer.
//
// The shared machinery is internal/harness; see its documentation for the
// scratch layout, the machine budget, and the failure diagnostics.
package health
