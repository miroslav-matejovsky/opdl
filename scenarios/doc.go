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
//     and registers a unit against it. A site of one is its own only expected
//     machine, so this is the shortest path from a blueprint to an accepted
//     registration.
//   - two_machine_eventfabric_test.go starts both machines of the "two-machine"
//     blueprint and checks they share one site journal, knowing about each other
//     only from the topology compiled into them, and that each is ready before
//     its API is.
//   - two_machine_registration_test.go is the same site carrying a registration
//     across both machines over REST, including a machine that was not there and
//     two claims on one key.
//   - resilience_test.go is the platform interrupted or unable to start: a killed
//     machine that must come back to the same answers, and a machine whose
//     journal storage is unusable, which must refuse to serve.
//   - warm_standby_test.go launches the packaged primary and standby, forces and
//     gracefully hands ownership over repeatedly, checks preferred-primary
//     reclamation and full shutdown, proves both processes share one machine
//     endpoint, and records timing and memory baselines.
//   - four_machine_storage_test.go is the three-storage-node topology proof: four
//     machines from one blueprint, of which exactly the first three by sorted
//     name store the journal and bind a cluster listener while the fourth is
//     client-only, publication and replay across machines, continued service
//     after a storage machine stops, and its rejoin onto its own storage.
//   - dotnet_sdk_e2e_test.go is the whole thing through the generated .NET SDK:
//     the builder, two platform processes, a real site journal, and a consumer.
//
// # Where the NATS ports come from
//
// A scenario builds from a rendered blueprint, not from checked-in HCL. The
// harness reserves free client and cluster ports on each machine's own loopback
// address, renders them into a temporary project.hcl through
// testdata/project.hcl.tmpl, and builds that. The reservations are held through
// rendering and building and released immediately before the first process that
// needs to bind them.
//
// This matters because it is the same contract a customer build uses. NATS
// endpoints are deployment topology: they are authored as ports in the blueprint
// and compiled into each machine's descriptor, and the runtime configuration
// cannot set them at all. An earlier harness passed them as runtime overrides
// instead, which exercised a path no deployment has and is what allowed a warm
// standby defect to survive a passing suite.
//
// Fixed ports in checked-in HCL would not do either: several machines share one
// host and a developer's machine may already hold 4222. The machine names,
// addresses, and standby policies live in the harness's fixture model, which is
// their single source; only the ports are decided per run.
//
// # Proving what does not listen
//
// Scenarios assert the absence of a listener through the runtime's own effective
// configuration output and the built artifacts, not by scanning the operating
// system's sockets. A socket scan is a flaky proof: it cannot distinguish a port
// this machine opened from one another test or a local process holds. The
// startup line naming each process's composed endpoints, together with unit
// tests of the embedded server options, is a deterministic contract instead.
//
// # Two machines and a machine that is not there
//
// The two-machine scenarios exist for one reason: a registration is accepted only
// once every machine the deployment declares has confirmed it. The interesting
// case is therefore an expected machine that is not running, which a
// single-machine site cannot express.
//
// Both start node A alone, so a proposal must sit pending and name node B as what
// it is waiting for, and only then start node B. Nothing hands node B the
// backlog: it replays the retained journal at startup and finds the proposal
// waiting for it, which is the whole point of a durable journal.
//
// In the SDK scenario the two halves are separated by a file marker: the .NET
// test writes it once its pending assertions have passed, and this harness starts
// node B when it appears. That is a handshake rather than a sleep because the
// fact being waited for is another process finishing an assertion, and no
// duration expresses that. It is also what makes the pending half mean anything:
// without the marker node B never starts, and the scenario fails instead of
// quietly passing.
//
// # Ports, storage, and who stores what
//
// A deployment derives every address from a machine's own IP on fixed ports, and
// places the journal's storage where site operations decide. A scenario cannot:
// several machines share one host, and those ports and paths are not the
// scenario's to take. So the harness reserves ephemeral ports and a temporary
// directory per machine and passes them as runtime overrides, which move sockets
// and storage and nothing else. Which machine a process is stays what it was
// built with.
//
// This forces the harness to know which machines store the site journal, because
// only those run a server and the rest are configured as clients of them. It
// derives that the same way the platform does — the first machine by sorted name
// for a site smaller than three — rather than being told, so a scenario cannot
// quietly disagree with the deployment about who stores what.
//
// # Evidence
//
// Domain behavior is checked through the platform's public API: the projected
// registration state every machine answers from, and the fact that a machine
// answers at all. Process lifecycle is checked through each role's local atomic
// status file, which is the deployment-tooling contract but never an ownership
// fence. The API signal is real rather than a liveness check, since
// the platform does not serve until its Event Fabric has connected, its
// projection has replayed the retained journal, and its handlers have worked
// through what was waiting for them.
//
// There are deliberately no local event files to read. The site journal is the
// platform's own storage, not a scenario's fixture, and inspecting it would mean
// asserting on the transport rather than on what the platform promises. Where a
// scenario needs to see the site agree with itself, it asks both machines and
// compares their answers.
//
// The API helpers deliberately re-declare the JSON shapes they read instead of
// importing the platform's Go types, so a scenario checks the published contract
// and a change to it fails here rather than silently recompiling.
//
// Restart scenarios force-stop a process because crash recovery is the promise
// they test. Warm-standby scenarios additionally use the operating system's
// process-control signal for planned handover and full shutdown. No runtime
// promotion endpoint exists. On Windows every launched command is placed in a
// kill-on-close job before it starts. This keeps nested builder, Go, and .NET
// processes inside the scenario lifecycle even when the test process itself is
// terminated by a hard timeout.
package scenarios
