// Package servicehealth probes the services one machine hosts and reports what
// it found.
//
// It is machine-level, not instance-level. A service runs on a machine, and
// both of that machine's platform instances probe it independently for their
// whole lifetimes: monitoring does not start at an activation and does not stop
// at a step-down. Two instances observing one service is the point rather than
// duplication — an observation from a process that has stopped and one from a
// service that has stopped must not look alike, and they only stay
// distinguishable while both observers are expected to report.
//
// # What a worker does
//
// One worker runs per target. It waits out a startup jitter, probes, folds the
// outcome into that target's stable status, hands the result to the sink, and
// waits one interval before probing again. Waiting after the attempt rather
// than on a fixed schedule is what keeps attempts from overlapping: a slow probe
// delays the next one instead of running beside it.
//
// The startup jitter is somewhere in [0, interval), derived from the observer's
// fixed instance role and the service name rather than drawn at random. Without
// it a machine's two instances probe every service on it in the same instant —
// a reboot starts both within the same second, and each then waits a fixed
// interval, so the pair stays in step for as long as both run. Deriving it
// rather than randomising it means a restarted instance resumes the phase it
// had, so a restart cannot move it onto its peer.
//
// A snapshot reaches the sink after every completed attempt, including one that
// changed nothing. That is deliberate. Distribution is at-most-once with no
// replay, so a report that says the same thing as the last one is what repairs
// a lost message and what tells a subscriber that started late where a service
// stands. Transitions alone could do neither.
//
// # Stable status and pending failure
//
// A target starts Unknown, because nothing has been asked yet and that is
// different from an answer. One success makes it Healthy and clears the failure
// count. A failure increments that count, and the target becomes Unhealthy only
// once the count reaches the authored retries.
//
// Between those, the stable status does not move: a service that has failed
// once out of three is still reported at whatever it last stably was, with the
// pending failure count beside it. That count is what lets a reader see a
// service beginning to fail without the platform having decided it has.
//
// Recovery is not symmetric with failure, and that is intentional. Failure is
// debounced because a single missed request is usually noise; recovery is not,
// because a service that answered is answering.
//
// # What is not a failure
//
// A probe cut short because the process is stopping is not a target failure.
// The service was never given a chance to answer, so recording one would end
// every deployment's health record with a fabricated outage.
//
// # Seams
//
// The clock, the prober, and the sink are injected. The state machine that
// folds outcomes into a stable status holds no timing at all, so what this
// package promises is testable without sleeping and without a socket.
//
// This package parses no deployment strings and imports no configuration. The
// composition root reads the descriptor and hands it typed durations and a
// resolved request URL.
package servicehealth
