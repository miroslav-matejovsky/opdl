// Package scenarios runs black-box, end-to-end checks of the distribution line:
// it drives the builder CLI as a subprocess to produce deployment packages, runs
// the resulting platform binaries as subprocesses, and checks their behavior from
// outside, never through internal APIs.
//
// It depends on no other module in this repository. The builder, every machine,
// and the .NET tests are external commands, driven the way a customer drives
// them, so a scenario proves the shipped artifacts rather than the code that
// happens to be linked into a test binary.
//
// # The scenarios
//
//   - build_and_run_test.go builds the one machine of the "scenario" blueprint
//     and registers a unit against it. A site of one accepts on its own, so this
//     is the shortest path from a blueprint to an accepted registration.
//   - two_machine_fabric_test.go starts both machines of the "two-machine"
//     blueprint and checks they form one fabric, knowing about each other only
//     from the topology compiled into them, and that each is ready before its API
//     is.
//   - two_machine_registration_test.go is the same site carrying registration
//     across both machines over REST.
//   - dotnet_sdk_e2e_test.go is the whole thing through the generated .NET SDK:
//     the builder, two platform processes, a real fabric, and a consumer.
//
// # Two machines and a machine that is not there
//
// The two-machine scenarios exist for one reason: a registration is accepted only
// once every machine the deployment declares has accepted it. The interesting
// case is therefore an expected machine that is not running, which a
// single-machine site cannot express.
//
// Both start node A alone, so a request must sit pending and name node B as what
// it is waiting for, and only then start node B. In the SDK scenario the two
// halves are separated by a file marker: the .NET test writes it once its pending
// assertions have passed, and this harness starts node B when it appears. That is
// a handshake rather than a sleep because the fact being waited for is another
// process finishing an assertion, and no duration expresses that. It is also what
// makes the pending half mean anything: without the marker node B never starts,
// and the scenario fails instead of quietly passing.
//
// # Evidence
//
// Behavior is checked against the platform's event log, not its process output. A
// registration's phases are facts the platform states, each recorded by the
// machine that owns the transition, so the log is the account to check: a
// scenario asserts that the story the events tell matches the one the API told.
// Each record is flushed before the operation that produced it answers, so a
// scenario that has read a response can already read the events it produced, and
// reads them while the process is alive: a force stop is not portable enough to
// rely on for flushing, and the orderly shutdown a signal would cause is covered
// by the platform's own in-process lifecycle tests.
//
// The event helpers deliberately re-declare the JSON shapes they read instead of
// importing the platform's Go types, so a scenario checks the published contract
// and a change to it fails here rather than silently recompiling.
package scenarios
