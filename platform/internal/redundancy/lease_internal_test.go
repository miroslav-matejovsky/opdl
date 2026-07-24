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

func TestLeaseAcquiresAFreeFileAndStartsAtGenerationOne(t *testing.T) {
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
	require.Equal(t, uint64(1), view.Generation)
	require.WithinDuration(t, now.Add(lease.cfg.Duration), view.Expiry, time.Millisecond)
}

func TestLeaseRenewExtendsExpiryWithoutChangingGeneration(t *testing.T) {
	t.Parallel()

	lease, err := OpenLease(testLeaseConfig(t), RolePrimary)
	require.NoError(t, err)
	now := time.Now()
	_, err = lease.tryAcquire(now)
	require.NoError(t, err)

	later := now.Add(300 * time.Millisecond)
	require.NoError(t, lease.renew(later))

	view := lease.View()
	require.Equal(t, uint64(1), view.Generation, "renewal keeps the same grant")
	require.WithinDuration(t, later.Add(lease.cfg.Duration), view.Expiry, time.Millisecond)
}

func TestLeaseTakeoverOfAnExpiredGrantIsAbandonedAndBumpsGeneration(t *testing.T) {
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
	require.Equal(t, uint64(2), standby.View().Generation, "a takeover bumps the generation")
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

	free, err := standby.free(now)
	require.NoError(t, err)
	require.False(t, free, "a held lease is not free")

	require.NoError(t, holder.release())
	require.False(t, holder.Held())

	free, err = standby.free(now)
	require.NoError(t, err)
	require.True(t, free, "a released lease is free at once, without waiting out the duration")

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
