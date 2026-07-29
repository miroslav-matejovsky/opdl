package servicehealth_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/servicehealth"
)

// These run against a real listener, because what the prober promises is about
// HTTP rather than about scheduling: which status codes pass, what happens to a
// redirect, and that a service which never answers does not hold a probe open
// past its timeout.

// probeTarget aims a target at a test server. The timings are the worker's
// business and are only here to make the target valid.
func probeTarget(url string) servicehealth.Target {
	return servicehealth.Target{
		Service:  "alarm-service",
		Role:     "master",
		URL:      url,
		Interval: 10 * time.Second,
		Timeout:  2 * time.Second,
		Retries:  3,
	}
}

// TestHTTPProberAcceptsOnly2xx pins what counts as a service saying it is well.
//
// Every other answer is one thing to the caller — the service did not say it was
// well — and which of them it was survives in the error text rather than in a
// distinction the retry policy would have to know about.
func TestHTTPProberAcceptsOnly2xx(t *testing.T) {
	tests := map[string]struct {
		status  int
		wantErr bool
	}{
		"200 OK":                    {status: http.StatusOK},
		"201 Created":               {status: http.StatusCreated},
		"204 No Content":            {status: http.StatusNoContent},
		"299":                       {status: 299},
		"301 Moved Permanently":     {status: http.StatusMovedPermanently, wantErr: true},
		"400 Bad Request":           {status: http.StatusBadRequest, wantErr: true},
		"404 Not Found":             {status: http.StatusNotFound, wantErr: true},
		"500 Internal Server Error": {status: http.StatusInternalServerError, wantErr: true},
		"503 Service Unavailable":   {status: http.StatusServiceUnavailable, wantErr: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()

			err := servicehealth.NewHTTPProber().Probe(t.Context(), probeTarget(server.URL+"/health"))
			if !test.wantErr {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, fmt.Sprintf("status %d", test.status),
				"the answer the service actually gave survives into the error a reader sees")
		})
	}
}

// TestHTTPProberRequestsTheAuthoredTarget checks the probe asks for exactly what
// the descriptor authored: a GET, at that path, with the query intact.
//
// The path is carried byte for byte from the blueprint through the descriptor to
// here, so a service that distinguishes its health endpoints by a query is
// probed at the one its author wrote.
func TestHTTPProberRequestsTheAuthoredTarget(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	require.NoError(t, servicehealth.NewHTTPProber().Probe(t.Context(), probeTarget(server.URL+"/Health/Ready?deep=1&verbose=1")))
	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/Health/Ready", gotPath, "the authored casing reaches the service")
	require.Equal(t, "deep=1&verbose=1", gotQuery)
}

// TestHTTPProberDoesNotFollowRedirects checks a health endpoint that answers 302
// has not said it is healthy.
//
// Following it would let a service report on an address nothing authored, and
// would make the probe's target something the deployment cannot see.
func TestHTTPProberDoesNotFollowRedirects(t *testing.T) {
	var healthyHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			healthyHits++
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer server.Close()

	err := servicehealth.NewHTTPProber().Probe(t.Context(), probeTarget(server.URL+"/health"))
	require.ErrorContains(t, err, "status 302")
	require.Zero(t, healthyHits, "the redirect was not followed")
}

// TestHTTPProberFailsWhenNothingIsListening checks a refused connection is a
// failed attempt carrying what went wrong.
func TestHTTPProberFailsWhenNothingIsListening(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL + "/health"
	server.Close()

	err := servicehealth.NewHTTPProber().Probe(t.Context(), probeTarget(url))
	require.Error(t, err)
	require.ErrorContains(t, err, url, "the error names the endpoint that did not answer")
}

// TestHTTPProberStopsAtItsDeadline checks a service that never answers does not
// hold a probe open.
//
// The worker bounds every attempt with the target's timeout, and this is the
// prober honouring it. A prober that ignored the context would let one slow
// service delay its own next attempt indefinitely.
func TestHTTPProberStopsAtItsDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err := servicehealth.NewHTTPProber().Probe(ctx, probeTarget(server.URL+"/health"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestHTTPProberBoundsTheBodyItReads checks a target that answers with far more
// than a health endpoint should cannot make a probe expensive.
//
// The body is not part of the answer — the status code is — and it is read at
// all only so the connection can go back to the pool. The service being probed
// is the one thing here not trusted to be small.
func TestHTTPProberBoundsTheBodyItReads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := strings.Repeat("x", 4<<10)
		for range 512 {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
			if r.Context().Err() != nil {
				return
			}
		}
	}))
	defer server.Close()

	require.NoError(t, servicehealth.NewHTTPProber().Probe(t.Context(), probeTarget(server.URL+"/health")),
		"a 2xx is healthy however much the service said afterwards")
}

// TestHTTPProberRejectsAnUnusableURL checks a target the composition root
// assembled wrongly fails as an attempt rather than panicking. Start already
// refuses these, so this is the second line rather than the first.
func TestHTTPProberRejectsAnUnusableURL(t *testing.T) {
	err := servicehealth.NewHTTPProber().Probe(t.Context(), probeTarget("http://%zz/health"))
	require.ErrorContains(t, err, "build probe request")
}
