// Package harness builds and runs the platform the way a customer would, so a
// scenario can drive it from outside and check its behavior as a black box.
//
// The harness renders a project blueprint with allocated ports, builds every
// machine of it, prepares each machine to run, and hands scenarios a Site of
// Machines they start, stop, restart, and question over the platform's public
// REST API and its local event records. It never imports platform code.
//
// # How a scenario builds
//
// The one build-time dependency is builder/build, which the harness calls
// in-process through build.Run. That runs the same flow the builder CLI a
// customer uses runs: load and validate the blueprint, resolve the per-machine
// plan, and compile a deployment package per machine. So a scenario proves the
// shipped artifacts rather than the code that happens to be linked into a test
// binary.
//
// The harness passes builder only scenario-owned inputs: the blueprint directory
// it rendered and the output directory to write packages to. Where the platform
// source lives and what product line it is are the builder's defaults, so the
// platform stays a black box on the build side too, not just at runtime.
//
// The supporting packages are all local: internal/procrun runs and supervises
// the child processes, internal/semaphore bounds the load, internal/waitfor
// polls, internal/logscan reads what a machine printed, and internal/processinfo
// measures one. Only testnet, the port allocator, still comes from utils, which
// platform shares.
//
// # Where the NATS ports come from
//
// A scenario builds from a rendered blueprint, not from checked-in HCL. The
// harness allocates free client and cluster ports from the fixed 20000 to 32767
// band, below the Windows ephemeral range that starts at 49152, and never reuses
// a port within a single run. Those ports are rendered into a temporary
// project.hcl through the embedded testdata/project.hcl.tmpl and built into the
// deployment packages.
//
// This matters because it is the same contract a customer build uses. NATS
// endpoints are deployment topology: they are authored as ports in the blueprint
// and compiled into each machine's descriptor, and the runtime configuration
// cannot set them at all. Fixed ports in checked-in HCL would not do either:
// several machines share one host and a developer's machine may already hold
// 4222.
//
// # Evidence
//
// Domain behavior is checked through the platform's public API: the projected
// registration state every machine answers from, and the fact that a machine
// answers at all. Process lifecycle is checked through each instance's local
// JSONL event record, which is the deployment-tooling contract but never an
// ownership lock: a scenario waits for the instance to state that it is serving
// or that it is ready to take over, matched on the PID the fact was stated
// under. The API signal is real rather than a liveness check, since the platform
// does not serve until its Event Fabric has connected, its projection has
// replayed the retained journal, and its handlers have worked through what was
// waiting for them.
//
// The API and manifest helpers deliberately re-declare the JSON shapes they read
// instead of importing the platform's or builder's Go types, so a scenario checks
// the published contract and a change to it fails here rather than silently
// recompiling.
//
// # Concurrency and resource budgeting
//
// Scenarios run concurrently under a bounded load budget. Each scenario is not a
// single unit of work: it compiles Go code, then runs between one and four
// platform processes, each embedding NATS JetStream and writing a journal to
// disk. The harness enforces two independent limits using the internal semaphore
// package:
//   - Build concurrency: bounded to 2 concurrent builds.
//   - Machine budget: bounded to max(2, GOMAXPROCS/2) concurrent platform
//     processes. DeploySite acquires weight equal to a project's machine count
//     and releases it in t.Cleanup, so a scenario author only calls t.Parallel().
//
// A scenario asking for more machines than the budget has is clamped to the whole
// budget rather than blocking on a limit it can never satisfy.
//
// # Where scenario artifacts live
//
// Every scenario artifact outlives the run inside
// scenarios/.tmp/<category>/<Scenario>/, emptied once on first access per run.
// The path is the scenario's name, so a subtest shares its scenario's root and
// two scenarios of one category do not share anything. The subdirectories are:
//   - blueprints/: the temporary project.hcl rendered for the build
//   - out/: the compiled packages and manifests produced by the builder
//   - work/: runtime configuration files, site journals, and each instance's
//     operational JSONL event record
//   - control/: marker files used for coordination (such as in the .NET SDK
//     scenario)
//
// Setting OPDL_SCENARIO_TMP relocates the base scratch root, so two runs of the
// suite can execute concurrently against independent directories.
package harness
