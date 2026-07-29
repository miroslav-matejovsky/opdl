package servicehealth

// tracker folds one target's attempt outcomes into a stable status.
//
// It holds no clock, no transport, and no target: what it knows is how many
// consecutive failures make a service down and what has happened since. That is
// what makes the policy this package is really about testable as a table, with
// nothing to wait for and nothing to stub.
//
// It is not safe for concurrent use, and does not need to be: one worker owns
// one tracker and records attempts one at a time.
type tracker struct {
	// retries is how many consecutive failures make the target Unhealthy.
	retries int
	// status is what the target is stably reported as right now.
	status Status
	// failures is how many attempts have failed in a row, reset by any success.
	failures int
}

// newTracker starts a target at Unknown, which is what it is before anything
// has asked it.
func newTracker(retries int) *tracker {
	return &tracker{retries: retries, status: StatusUnknown}
}

// succeeded records an attempt that reached the service and got an acceptable
// answer.
//
// One success is enough, whatever came before it. Failure is debounced because
// a single missed request is usually noise; recovery is not, because a service
// that answered is answering, and holding it down for two more intervals would
// report an outage that has ended.
func (t *tracker) succeeded() {
	t.status = StatusHealthy
	t.failures = 0
}

// failed records an attempt that did not.
//
// The count stops at the threshold rather than climbing forever. Past it the
// status already carries the fact, and an unbounded counter would be the one
// value in an observation that grows without limit for as long as a service
// stays down.
func (t *tracker) failed() {
	if t.failures < t.retries {
		t.failures++
	}
	if t.failures >= t.retries {
		t.status = StatusUnhealthy
	}
}

// record folds one attempt in and returns what to report.
//
// The status returned is the stable one, not the attempt's own outcome: a
// failure below the threshold leaves a Healthy target Healthy, and the pending
// count beside it is what shows it is on its way down.
func (t *tracker) record(err error) (status Status, pending int) {
	if err == nil {
		t.succeeded()
	} else {
		t.failed()
	}
	return t.status, t.failures
}
