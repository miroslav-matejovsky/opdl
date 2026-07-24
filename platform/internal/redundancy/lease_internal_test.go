package redundancy

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These test the lease record and the file operations on it directly, which is
// why they live in package redundancy: acquire, renew, and release are the
// ownership primitive, and their CAS, expiry, and generation behavior is what the
// higher-level lifecycle in ownership.go depends on.

func testLeaseConfig(t *testing.T) LeaseConfig {
	t.Helper()
	return LeaseConfig{
		File:                filepath.Join(t.TempDir(), "lease"),
		Duration:            time.Second,
		RenewalInterval:     200 * time.Millisecond,
		HealthCheckInterval: 50 * time.Millisecond,
	}
}

func TestLeaseAcquiresAFreeFile(t *testing.T) {
	t.Parallel()

	lease, err := OpenLease(testLeaseConfig(t), RolePrimary)
	require.NoError(t, err)

	now := time.Now()
	acquired, err := lease.tryAcquire(now)
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.False(t, acquired.Abandoned, "there was no prior owner to abandon anything")
	require.True(t, lease.Held())

	view := lease.View()
	require.True(t, view.Held)
	require.WithinDuration(t, now.Add(lease.cfg.Duration), view.Expiry, time.Millisecond)
}

func TestLeaseRenewExtendsExpiry(t *testing.T) {
	t.Parallel()

	lease, err := OpenLease(testLeaseConfig(t), RolePrimary)
	require.NoError(t, err)
	now := time.Now()
	_, err = lease.tryAcquire(now)
	require.NoError(t, err)

	later := now.Add(300 * time.Millisecond)
	require.NoError(t, lease.renew(later))

	view := lease.View()
	require.WithinDuration(t, later.Add(lease.cfg.Duration), view.Expiry, time.Millisecond)
}

func TestLeaseTakeoverOfAnExpiredGrantIsAbandoned(t *testing.T) {
	t.Parallel()

	cfg := testLeaseConfig(t)
	holder, err := OpenLease(cfg, RolePrimary)
	require.NoError(t, err)
	standby, err := OpenLease(cfg, RoleStandby)
	require.NoError(t, err)

	start := time.Now()
	_, err = holder.tryAcquire(start)
	require.NoError(t, err)

	// The standby cannot take a valid grant.
	acquired, err := standby.tryAcquire(start.Add(100 * time.Millisecond))
	require.NoError(t, err)
	require.False(t, acquired.Held, "a valid lease is not free")

	// Once the grant has expired, the standby takes it and reports the abandonment.
	acquired, err = standby.tryAcquire(start.Add(cfg.Duration + time.Millisecond))
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.True(t, acquired.Abandoned, "a lapsed grant was not handed over")
	require.True(t, standby.Held())
}

func TestLeaseReleaseFreesTheGrantForThePeer(t *testing.T) {
	t.Parallel()

	cfg := testLeaseConfig(t)
	holder, err := OpenLease(cfg, RolePrimary)
	require.NoError(t, err)
	standby, err := OpenLease(cfg, RoleStandby)
	require.NoError(t, err)

	now := time.Now()
	_, err = holder.tryAcquire(now)
	require.NoError(t, err)

	avail, owner, err := standby.observe(now)
	require.NoError(t, err)
	require.Equal(t, leaseHeld, avail, "a valid grant is not promotable")
	require.Equal(t, RolePrimary, owner)

	require.NoError(t, holder.release())
	require.False(t, holder.Held())

	avail, owner, err = standby.observe(now)
	require.NoError(t, err)
	require.Equal(t, leaseReleased, avail, "a released lease is a handover at once, without waiting out the duration")
	require.Equal(t, RolePrimary, owner, "the released grant still names who gave it up")

	// A takeover of a released grant is a clean handover, not an abandonment.
	acquired, err := standby.tryAcquire(now)
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.False(t, acquired.Abandoned)
}

func TestLeaseRenewAfterAPeerTookOverReportsOwnershipLost(t *testing.T) {
	t.Parallel()

	cfg := testLeaseConfig(t)
	holder, err := OpenLease(cfg, RolePrimary)
	require.NoError(t, err)
	standby, err := OpenLease(cfg, RoleStandby)
	require.NoError(t, err)

	start := time.Now()
	_, err = holder.tryAcquire(start)
	require.NoError(t, err)

	// The standby takes over once the grant lapses.
	acquired, err := standby.tryAcquire(start.Add(cfg.Duration + time.Millisecond))
	require.NoError(t, err)
	require.True(t, acquired.Held)

	// The former holder, coming back, finds it no longer owns the lease.
	err = holder.renew(start.Add(cfg.Duration + 2*time.Millisecond))
	require.ErrorIs(t, err, errOwnershipLost)
	require.False(t, holder.Held())
}

func TestOpenLeaseRejectsUnusableTimings(t *testing.T) {
	t.Parallel()

	base := testLeaseConfig(t)

	renewTooLong := base
	renewTooLong.RenewalInterval = base.Duration
	_, err := OpenLease(renewTooLong, RolePrimary)
	require.ErrorContains(t, err, "renewal interval")

	zero := base
	zero.Duration = 0
	_, err = OpenLease(zero, RolePrimary)
	require.ErrorContains(t, err, "must be positive")
}

func TestOpenLeaseWithNoFileIsNilAndActiveByConstruction(t *testing.T) {
	t.Parallel()

	lease, err := OpenLease(LeaseConfig{}, RolePrimary)
	require.NoError(t, err)
	require.Nil(t, lease)

	require.True(t, lease.Held(), "a standby-less machine is Active by construction")
	require.Equal(t, RolePrimary, lease.Role())
	require.Equal(t, "", lease.File())
	require.True(t, lease.View().Held)

	acquired, err := lease.tryAcquire(time.Now())
	require.NoError(t, err)
	require.True(t, acquired.Held)
	require.NoError(t, lease.renew(time.Now()))
	require.NoError(t, lease.release())
}
