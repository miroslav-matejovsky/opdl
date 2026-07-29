package servicehealth

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// maxProbeBody is how much of a health response is read before the rest is
// discarded.
//
// The body is not part of the answer: what decides an attempt is the status
// code. It is read at all so the connection can be reused rather than torn down
// after every probe, and it is bounded because the service being probed is the
// one thing here that is not trusted to be small — a target that streamed
// forever would otherwise hold a probe open until its timeout on every attempt.
const maxProbeBody = 4 << 10

// HTTPProber probes a service by requesting its health URL and reading the
// status code.
//
// One prober is shared by every worker on the machine. Its transport pools
// connections, so a service probed every few seconds is usually reached on a
// connection already open, and the cost of a probe stays closer to a request
// than to a handshake.
type HTTPProber struct {
	client *http.Client
}

// NewHTTPProber builds the prober a running process uses.
//
// Three things are turned off deliberately. Redirects are not followed: a
// health endpoint that answers 302 has not said it is healthy, and following it
// would let a service report on an address nothing authored. The proxy is
// disabled because the target is on this machine's own ip and an inherited
// HTTP_PROXY would send a local probe through a remote hop, turning a machine's
// own health into a question about the network. And no client timeout is set
// here: the worker bounds each attempt with the target's own, which is per
// target where this is per process.
func NewHTTPProber() *HTTPProber {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &HTTPProber{
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Probe fetches the target's health URL and reports whether it answered
// acceptably.
//
// Success is a 2xx status and nothing else. A connection refused, a timeout, a
// malformed response, and a 500 are all one thing to the caller — the service
// did not say it was well — and which of them it was survives in the error text
// for whoever reads it.
func (p *HTTPProber) Probe(ctx context.Context, target Target) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, http.NoBody)
	if err != nil {
		return fmt.Errorf("build probe request: %w", err)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("probe %s: %w", target.URL, err)
	}
	defer func() { _ = response.Body.Close() }()
	// Drained before the status is judged, so the connection goes back to the
	// pool on a failed probe exactly as it does on a healthy one. A service that
	// is answering 500 is the case where reusing connections matters most.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxProbeBody))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("probe %s: status %s", target.URL, response.Status)
	}
	return nil
}
