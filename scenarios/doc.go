// Package scenarios is the root of the black-box scenario suite: it builds
// deployment packages, runs the resulting platform binaries as subprocesses, and
// checks their behavior from outside, never through internal APIs.
//
// It holds no code of its own. The suite is a program rather than a test binary:
//
//   - cmd is the command that runs it. Parallelism, the timeout, selection, and
//     verbosity are its flags.
//   - internal/runner turns registered scenarios into real Go tests, so a
//     scenario keeps t.Parallel, t.Cleanup, t.Run, and require.
//   - internal/harness is the shared machinery: it renders a blueprint, builds it
//     in-process through builder/build, and runs the machines it produces.
//   - internal/procrun, internal/semaphore, internal/waitfor, internal/logscan,
//     and internal/processinfo are the supporting packages. Only utils/testnet is
//     still shared with other modules.
//
// The scenarios themselves live in one package per category, and each
// contributes a runner.Set through its Scenarios function that cmd collects:
//
//   - smoke builds and runs the smallest deployment the platform will start.
//   - redundancy drives one machine's two instances through failover and
//     failback.
//   - health deploys two machines with services it controls and checks that
//     every instance converges on the same picture of them.
//   - sdk drives a running instance through the generated .NET client.
//
// A category package never depends on another. A helper two categories want
// belongs in the harness.
//
// Nothing here reaches into platform or builder internals. The platform is a
// black box reached through its packaged binaries, and the blueprint goes to the
// builder as an argument.
package scenarios
