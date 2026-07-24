package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/redundancy"
)

// This file adapts the descriptor's lease policy and the peer's health endpoint
// into what the redundancy package's ownership machine needs, so redundancy makes
// no network call and parses no configuration string itself.

// peerHealthTimeout bounds one peer health probe. It is short because the probe is
// a loopback GET the promotion loop makes every health-check interval, and a peer
// that cannot answer within it is exactly the unhealthy peer the gate is looking
// for.
const peerHealthTimeout = time.Second

// leaseConfigOf builds the runtime lease policy from the descriptor's lease block.
// A machine that deploys no Standby Instance carries no lease, so this returns the
// zero config, which OpenLease turns into a nil Lease that is Active by
// construction.
//
// The descriptor's failback_stabilization is not read here: this pass implements
// no failback, so nothing would act on it. It is carried in the descriptor for
// when failback lands; see docs/plans/redundancy-rest.md.
func leaseConfigOf(descriptor config.Descriptor) (redundancy.LeaseConfig, error) {
	lease := descriptor.Lease
	if lease == nil {
		return redundancy.LeaseConfig{}, nil
	}
	duration, err := time.ParseDuration(lease.Duration)
	if err != nil {
		return redundancy.LeaseConfig{File: lease.File}, fmt.Errorf("lease duration %q: %w", lease.Duration, err)
	}
	renewal, err := time.ParseDuration(lease.RenewalInterval)
	if err != nil {
		return redundancy.LeaseConfig{File: lease.File}, fmt.Errorf("lease renewal_interval %q: %w", lease.RenewalInterval, err)
	}
	healthCheck, err := time.ParseDuration(lease.HealthCheckInterval)
	if err != nil {
		return redundancy.LeaseConfig{File: lease.File}, fmt.Errorf("lease health_check_interval %q: %w", lease.HealthCheckInterval, err)
	}
	return redundancy.LeaseConfig{
		File:                lease.File,
		Duration:            duration,
		RenewalInterval:     renewal,
		HealthCheckInterval: healthCheck,
	}, nil
}

// leaseViewOf renders this instance's ownership for the /health/ha endpoint. It is
// nil-safe: a standby-less machine holds ownership by construction, with no
// expiry and no generation.
func leaseViewOf(lease *redundancy.Lease) api.LeaseView {
	view := lease.View()
	out := api.LeaseView{Owned: view.Held, Generation: int64(view.Generation)}
	if !view.Expiry.IsZero() {
		expiry := view.Expiry.UTC().Format(time.RFC3339)
		out.ExpirationUTC = &expiry
	}
	return out
}

// peerHealthCheck builds the health gate a Passive instance promotes through: a
// probe of the machine's other instance's health endpoint. It returns nil when
// there is no peer to check, in which case the ownership machine never consults
// it.
//
// A peer that refuses the connection, times out, answers with a non-200, or
// reports itself Unhealthy is treated as unhealthy — every way a promoter learns
// the other instance can no longer serve.
func peerHealthCheck(_ *config.Config, peerAddress string) func(context.Context) bool {
	if peerAddress == "" {
		return nil
	}
	client := &http.Client{Timeout: peerHealthTimeout}
	url := "http://" + peerAddress + api.PathHealth
	return func(ctx context.Context) bool {
		reqCtx, cancel := context.WithTimeout(ctx, peerHealthTimeout)
		defer cancel()
		request, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		if err != nil {
			return false
		}
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return false
		}
		var health api.HealthResponse
		if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
			// It answered 200 but its body did not parse. It is reachable and
			// serving, which is what the gate is asking about.
			return true
		}
		return health.Status != api.HealthStatusUnhealthy
	}
}
