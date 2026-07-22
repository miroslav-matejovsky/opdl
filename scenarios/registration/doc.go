// Package registration holds the scenarios about what a site does with a claim.
//
// A registration is proposed to one machine and decided by all of them: the
// journal orders the claims, every machine of the declared membership confirms,
// and only then is the claim accepted. These scenarios drive that from outside,
// through REST, against binaries the builder packaged.
//
// BuildAndRunMinimumSite is the smallest site the platform accepts, so it is
// also the suite's smoke test: it proves a packaged binary boots, serves, and
// decides. TwoMachineRegistration is the acceptance barrier itself, including
// what the site reports while a machine it is waiting for is not running.
//
// The shared machinery is internal/harness; see its documentation for the
// scratch layout, the machine budget, and the failure diagnostics.
package registration
