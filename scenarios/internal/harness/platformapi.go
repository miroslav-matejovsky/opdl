package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file is the scenario side of the platform's HTTP API: enough to call it
// and read what came back.
//
// It is a black-box view. It re-declares the JSON shapes it needs rather than
// importing the platform's Go types, so a scenario checks the published contract
// and a change to it shows up here as a failing scenario rather than as a
// silently recompiled struct.

const (
	// apiPollInterval is how often a wait retries a request.
	apiPollInterval = 100 * time.Millisecond
	// APIWaitTimeout bounds a wait for the API to answer or to reach a state.
	// It is generous: it covers a machine replaying the site journal before it
	// reports itself active.
	APIWaitTimeout = 60 * time.Second
)

// The two states an instance reports for itself. A Passive instance answers
// /instance and refuses every domain operation; an Active one serves the whole
// API.
const (
	// InstanceStatePassive is an instance that is not serving domain operations.
	InstanceStatePassive = "passive"
	// InstanceStateActive is an instance that is.
	InstanceStateActive = "active"
)

// Instance is the observable shape of GET /instance: who this process is and
// what it is currently doing.
//
// None of it comes from the journal, which is why the endpoint answers from the
// moment the listener is bound, in every state, including a Passive instance
// that has not caught up and one that never will.
type Instance struct {
	// Machine is the descriptor machine this instance runs on.
	Machine string `json:"machine"`
	// Role is the fixed build-time instance role: primary or standby.
	Role string `json:"role"`
	// State is the current runtime state: active or passive.
	State string `json:"state"`
	// Address is this instance's own loopback API address.
	Address string `json:"address"`
	// PeerAddress is the machine's other instance's API address, empty on a
	// machine that deploys only a Primary Instance.
	PeerAddress string `json:"peer_address,omitempty"`
}

// getJSON issues a GET and decodes a 200 body into T. A non-200 is returned as
// a code with no value. Transport failures are returned, never asserted, so
// this is safe to call from inside a poll.
func getJSON[T any](ctx context.Context, url string) (value T, code int, err error) {
	var zero T
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return zero, 0, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return zero, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return zero, response.StatusCode, nil
	}
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return zero, response.StatusCode, err
	}
	return value, response.StatusCode, nil
}

// fetchInstance returns one machine's instance record, the status code it
// answered with, and any transport failure.
//
// It reports rather than asserts, and that is not a style preference: a poll
// condition that called require.* would call runtime.Goexit on the goroutine
// running it, stopping the wait dead and hiding the single transport error that
// was the actual failure.
func fetchInstance(ctx context.Context, m *Machine) (Instance, int, error) {
	return getJSON[Instance](ctx, m.URL+"/instance")
}

// FetchInstanceAt returns the instance record served at baseURL, the status
// code, and any transport failure. It reports rather than asserts, so it is
// safe inside a poll — a redundancy scenario polls both of a machine's
// instances at their own addresses, and either may legitimately be down.
func FetchInstanceAt(ctx context.Context, baseURL string) (Instance, int, error) {
	return getJSON[Instance](ctx, baseURL+"/instance")
}

// Health is the observable shape of GET /health: the primary operational health
// assessment every instance serves in every state.
type Health struct {
	// Status is Healthy, Degraded, or Unhealthy.
	Status string `json:"status"`
	// Role is the instance's fixed role, Primary or Standby spelled as the
	// runtime reports it.
	Role string `json:"role"`
	// RuntimeState is the instance's current state, active or passive.
	RuntimeState string `json:"runtimeState"`
}

// FetchHealthAt returns the health assessment served at baseURL, the status
// code, and any transport failure. Like FetchInstanceAt it reports rather than
// asserts, so it is safe inside a poll.
func FetchHealthAt(ctx context.Context, baseURL string) (Health, int, error) {
	return getJSON[Health](ctx, baseURL+"/health")
}

// GetInstance returns one machine's instance record and the status code it
// answered with. A machine that does not answer at all is a failure here, so
// this is for the test goroutine; a poll wants fetchInstance.
func GetInstance(ctx context.Context, t *testing.T, m *Machine) (instance Instance, code int) {
	t.Helper()
	instance, code, err := fetchInstance(ctx, m)
	require.NoErrorf(t, err, "%s did not answer for its instance record:%s", m.Name, Diagnostics(m))
	return instance, code
}

// WaitForActiveInstance blocks until a machine's Primary Instance reports that
// it is Active.
//
// That is the scenario's readiness signal, and it is a real one: the platform
// answers /instance from the moment its listener is bound, but it reports active
// only once its Event Fabric has connected, its projection has replayed the
// retained journal, and its handlers have worked through what was waiting for
// them. An active answer means all of that already happened.
func WaitForActiveInstance(ctx context.Context, t *testing.T, m *Machine) {
	t.Helper()
	var poll LastPoll
	cond := func() bool {
		instance, code, err := fetchInstance(ctx, m)
		poll.Record(instance, code, err)
		return err == nil && code == http.StatusOK && instance.State == InstanceStateActive
	}
	abort := func() (bool, string) {
		if m.Exited() {
			return true, fmt.Sprintf("%s exited before it was active:\n%s", m.Name, m.Output())
		}
		return false, ""
	}
	diag := DiagStringer(func() string {
		return fmt.Sprintf("%s never reported itself active; %s\n%s", m.Name, &poll, m.Output())
	})
	WaitFor(t, m.Name+" reporting itself active", APIWaitTimeout, apiPollInterval, cond, abort, diag)
}

// LastPoll is what the most recently completed poll saw.
//
// It is mutex-guarded because a poll condition and the failure message that reads
// its fields can run on different goroutines. An earlier shape passed &code into
// the message instead, which printed the pointer's address rather than the status
// ("last code 49112166178816").
type LastPoll struct {
	mu       sync.Mutex
	code     int
	err      error
	instance Instance
	seen     bool
}

// Record stores what a completed poll saw.
func (l *LastPoll) Record(instance Instance, code int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.instance, l.code, l.err, l.seen = instance, code, err, true
}

// String says what the last poll actually got. The distinction worth preserving
// is between a machine that answered with a state the wait did not want and one
// that did not answer at all: the second is not a slow start, it is a machine
// that is not listening.
func (l *LastPoll) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case !l.seen:
		return "no poll ever completed"
	case l.err != nil:
		return fmt.Sprintf("last request failed: %v", l.err)
	case l.code == http.StatusOK:
		return fmt.Sprintf("last response HTTP %d, instance %+v", l.code, l.instance)
	default:
		return fmt.Sprintf("last response HTTP %d", l.code)
	}
}
