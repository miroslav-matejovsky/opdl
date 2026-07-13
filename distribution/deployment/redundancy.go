package deployment

import (
	"fmt"
	"strings"
	"time"
)

// RedundancyMode declares whether service instances may process work together or
// require one fenced active owner.
type RedundancyMode string

const (
	// RedundancyActiveActive permits every assigned instance to run work.
	RedundancyActiveActive RedundancyMode = "active-active"
	// RedundancySingleActive permits only the lease owner to run work.
	RedundancySingleActive RedundancyMode = "single-active"
)

// RedundancyPolicy is static service activation policy resolved into every
// descriptor and authority roster. Empty policy means active-active.
type RedundancyPolicy struct {
	// Mode is active-active or single-active. Empty means active-active.
	Mode RedundancyMode `json:"mode,omitempty"`
	// Group identifies one static single-active election group.
	Group string `json:"group,omitempty"`
	// Scope identifies the fixed coordination scope. Step 5 supports site only.
	Scope string `json:"scope,omitempty"`
	// Members are machines hosting eligible instances, in deterministic authored
	// order. Runtime never discovers or changes this set.
	Members []string `json:"members,omitempty"`
	// LeaseDuration bounds unrenewed active ownership.
	LeaseDuration string `json:"lease_duration,omitempty"`
	// RetryInitial is first passive acquisition retry delay.
	RetryInitial string `json:"retry_initial,omitempty"`
	// RetryMax caps passive acquisition backoff.
	RetryMax string `json:"retry_max,omitempty"`
	// FailoverTimeout bounds waiting for an expired owner before reporting failure.
	FailoverTimeout string `json:"failover_timeout,omitempty"`
	// DrainTimeout bounds graceful active-work drain before lease release.
	DrainTimeout string `json:"drain_timeout,omitempty"`
}

// IsSingleActive reports whether p requires a fenced active owner.
func (p RedundancyPolicy) IsSingleActive() bool {
	return p.Mode == RedundancySingleActive
}

// Validate checks policy coherence before a descriptor can start. Membership
// placement consistency is validated by topology and roster validation.
func (p RedundancyPolicy) Validate() error {
	switch p.Mode {
	case "", RedundancyActiveActive:
		if p.Group != "" || p.Scope != "" || len(p.Members) != 0 ||
			p.LeaseDuration != "" || p.RetryInitial != "" || p.RetryMax != "" ||
			p.FailoverTimeout != "" || p.DrainTimeout != "" {
			return fmt.Errorf("redundancy: active-active must not declare election settings")
		}
		return nil
	case RedundancySingleActive:
	default:
		return fmt.Errorf("redundancy: mode must be active-active or single-active")
	}

	if strings.TrimSpace(p.Group) == "" {
		return fmt.Errorf("redundancy: single-active group is required")
	}
	if p.Scope != "site" {
		return fmt.Errorf("redundancy: single-active scope must be site")
	}
	if len(p.Members) < 2 {
		return fmt.Errorf("redundancy: single-active group must declare at least two members")
	}
	seen := make(map[string]bool, len(p.Members))
	for _, member := range p.Members {
		if strings.TrimSpace(member) == "" {
			return fmt.Errorf("redundancy: member is required")
		}
		if seen[member] {
			return fmt.Errorf("redundancy: member %q appears more than once", member)
		}
		seen[member] = true
	}
	leaseDuration, err := parseRedundancyDuration("lease_duration", p.LeaseDuration)
	if err != nil {
		return err
	}
	retryInitial, err := parseRedundancyDuration("retry_initial", p.RetryInitial)
	if err != nil {
		return err
	}
	retryMax, err := parseRedundancyDuration("retry_max", p.RetryMax)
	if err != nil {
		return err
	}
	if retryInitial > retryMax {
		return fmt.Errorf("redundancy: retry_initial must not exceed retry_max")
	}
	if _, err := parseRedundancyDuration("failover_timeout", p.FailoverTimeout); err != nil {
		return err
	}
	if _, err := parseRedundancyDuration("drain_timeout", p.DrainTimeout); err != nil {
		return err
	}
	if retryInitial >= leaseDuration {
		return fmt.Errorf("redundancy: retry_initial must be shorter than lease_duration")
	}
	return nil
}

func parseRedundancyDuration(name, value string) (time.Duration, error) {
	if value == "" {
		return 0, fmt.Errorf("redundancy: %s is required", name)
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("redundancy: %s must be a positive duration", name)
	}
	return duration, nil
}
