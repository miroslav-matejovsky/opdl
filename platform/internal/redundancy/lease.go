package redundancy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/miroslav-matejovsky/opdl/utils/atomicfile"
)

// This file is the lease: the machine-wide record a machine's two instances
// take turns holding, and the operations on it. Holding an unexpired lease is what makes
// an instance Active; nothing else does. What holding it means at runtime — when
// each state is entered and left — is ownership, and that is ownership.go.
//
// The lease replaces the non-expiring Windows named mutex the redundancy package
// used before. The mutex gave mutual exclusion by construction but could never
// fail over from an unresponsive-but-alive holder. The lease trades the kernel
// guarantee for a finite, renewable record so that a hung Active instance loses
// ownership on expiry and a Standby can take over. The single fact that keeps
// this safe is that a machine's two instances are two processes on one host: they
// share a clock and a filesystem, so an expiry compared against the local clock
// means the same instant to both, and there is no skew or partition to reason
// about. Two limitations are accepted deliberately: acquisition is
// write-then-confirm rather than a true compare-and-swap (the health gate and
// pre-expiry step-down make the race practically unreachable on one host), and
// expiry uses the wall clock, so a large backward clock step during a takeover
// window is unguarded.

// LeaseConfig is the machine's resolved lease policy: where ownership is recorded
// and the timings that govern how it is held and turned over. It is derived from
// the deployment descriptor's lease block and validated before it reaches here.
type LeaseConfig struct {
	// File is the machine-wide lease file both instances read and write.
	File string
	// Duration is how long a granted lease is valid without renewal.
	Duration time.Duration
	// RenewalInterval is how often the owner extends the lease. It is also the
	// step-down grace: an owner that cannot renew stops being Active one renewal
	// interval before its lease would expire, so a promoter never sees the lease
	// expired while this instance still believes it is Active.
	RenewalInterval time.Duration
	// HealthCheckInterval is how often a Passive instance evaluates promotion and
	// polls its peer's health, and how often an Active Standby polls the Primary
	// to decide whether to fail back.
	HealthCheckInterval time.Duration
	// FailbackStabilization is how long the Primary must be continuously healthy
	// before an Active Standby hands ownership back to it. Zero disables failback.
	FailbackStabilization time.Duration
}

// Lease is a machine's Primary Ownership handle. It reads and writes the shared
// lease file and tracks whether this process currently holds ownership.
//
// A nil Lease is the standby-less machine: there is no second instance to take it
// with, so the lone Primary Instance is Active by construction. Every method is
// nil-safe and reports ownership held.
//
// A Lease is safe for concurrent use: the renewal loop writes it while the health
// endpoint reads its view.
type Lease struct {
	cfg  LeaseConfig
	role InstanceRole
	pid  int

	mu    sync.Mutex
	held  bool
	owned record
}

// record is one lease grant as it is stored on disk. Every field is written and
// read on one host, so its timestamps and a reader's clock are the same clock.
type record struct {
	// OwnerRole and OwnerPID identify the holder, for diagnostics and so an owner
	// recognizes its own grant. OwnerPID is what distinguishes two instances
	// writing a free lease at the same instant.
	OwnerRole string `json:"owner_role"`
	OwnerPID  int    `json:"owner_pid"`
	// ExpiresUnixNano is when the grant stops being valid without renewal.
	ExpiresUnixNano int64 `json:"expires_unix_nano"`
	// Released marks a grant the owner gave up cleanly, so a promoter can tell a
	// graceful handover from a holder that simply died and let its lease lapse.
	Released bool `json:"released,omitempty"`
}

// expired reports whether the grant has passed its expiry.
func (r record) expired(now time.Time) bool { return now.UnixNano() >= r.ExpiresUnixNano }

// free reports whether the grant is available to acquire: released by its owner
// or lapsed.
func (r record) free(now time.Time) bool { return r.Released || r.expired(now) }

// errOwnershipLost is renew's report that the lease no longer names this instance:
// another instance acquired it. The owner must step down.
var errOwnershipLost = errors.New("redundancy: lease ownership lost")

// OpenLease prepares this instance to take part in the machine's ownership lease.
//
// When cfg.File is empty (the machine deploys no Standby Instance), it returns a
// nil Lease, which is Active by construction and nil-safe on every method.
//
// A non-empty file has its parent directory created, so the first acquisition can
// write it, and its timings validated, so a descriptor that reached here with a
// zero duration fails at startup rather than when ownership first changes hands.
func OpenLease(cfg LeaseConfig, role InstanceRole) (*Lease, error) {
	if !role.Valid() {
		return nil, fmt.Errorf("redundancy: open lease: invalid instance role %q", role)
	}
	if cfg.File == "" {
		return nil, nil //nolint:nilnil // a nil Lease when standby is disabled is intentional and nil-safe by contract
	}
	if cfg.Duration <= 0 || cfg.RenewalInterval <= 0 || cfg.HealthCheckInterval <= 0 {
		return nil, fmt.Errorf("redundancy: open lease: durations must be positive (duration=%s renewal=%s health_check=%s)", cfg.Duration, cfg.RenewalInterval, cfg.HealthCheckInterval)
	}
	if cfg.RenewalInterval >= cfg.Duration {
		return nil, fmt.Errorf("redundancy: open lease: renewal interval %s must be shorter than duration %s", cfg.RenewalInterval, cfg.Duration)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.File), 0o755); err != nil {
		return nil, fmt.Errorf("redundancy: open lease: create lease directory: %w", err)
	}
	return &Lease{cfg: cfg, role: role, pid: os.Getpid()}, nil
}

// tryAcquire takes ownership if the lease is free, and reports whether it did.
//
// It reads the current grant, and, if there is none or it is free, writes its
// successor and confirms by re-reading that this instance is the writer that
// landed. The confirm is what makes two instances writing a free lease at the
// same instant resolve to one owner without a kernel lock: the two instances
// have distinct roles, so each writes its own role, the file ends as one of them,
// and the loser sees the other's role on re-read and reports it did not acquire.
func (l *Lease) tryAcquire(now time.Time) (Acquisition, error) {
	if l == nil {
		return Acquisition{Held: true}, nil
	}
	cur, exists, err := readRecord(l.cfg.File)
	if err != nil {
		return Acquisition{}, err
	}
	if exists && !cur.free(now) {
		return Acquisition{}, nil // a valid lease is held by the other instance
	}
	next := record{
		OwnerRole:       string(l.role),
		OwnerPID:        l.pid,
		ExpiresUnixNano: now.Add(l.cfg.Duration).UnixNano(),
	}
	if err := writeRecord(l.cfg.File, next); err != nil {
		return Acquisition{}, err
	}
	confirmed, _, err := readRecord(l.cfg.File)
	if err != nil {
		return Acquisition{}, err
	}
	if confirmed.OwnerRole != string(l.role) {
		return Acquisition{}, nil // the other instance's write landed last; it owns
	}
	l.setOwned(next)
	// Ownership taken from a grant that lapsed without a clean release is a
	// takeover from a process that did not hand over; a released grant is not.
	return Acquisition{Held: true, Abandoned: exists && !cur.Released}, nil
}

// renew extends this instance's grant. It returns errOwnershipLost when the lease
// no longer names this instance, which the caller reads as "step down", and a
// plain error for a transient read or write failure it may retry.
func (l *Lease) renew(now time.Time) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	owned, held := l.owned, l.held
	l.mu.Unlock()
	if !held {
		return errOwnershipLost
	}
	cur, exists, err := readRecord(l.cfg.File)
	if err != nil {
		return err
	}
	if !exists || cur.OwnerRole != owned.OwnerRole {
		l.markLost()
		return errOwnershipLost
	}
	next := cur
	next.ExpiresUnixNano = now.Add(l.cfg.Duration).UnixNano()
	next.Released = false
	if err := writeRecord(l.cfg.File, next); err != nil {
		return err
	}
	l.setOwned(next)
	return nil
}

// release gives up ownership cleanly, writing a released grant so the peer
// promotes without waiting out the whole duration. It only writes when the lease
// still names this instance, so a release after ownership was lost cannot clobber
// the new owner. It is nil-safe and idempotent.
func (l *Lease) release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	owned, held := l.owned, l.held
	l.held = false
	l.mu.Unlock()
	if !held {
		return nil
	}
	cur, exists, err := readRecord(l.cfg.File)
	if err != nil {
		return err
	}
	if !exists || cur.OwnerRole != owned.OwnerRole {
		return nil // ownership already moved on; nothing to release
	}
	released := cur
	released.Released = true
	return writeRecord(l.cfg.File, released)
}

// availability classifies what a promoter reads from the lease file. The
// distinction between a released and a lapsed grant, and whose it was, is what
// promotion decisions are made from: a grant the other instance released is an
// explicit handover, a grant that lapsed is an owner that stopped renewing.
type availability int

const (
	// leaseHeld is a valid grant; the owner is serving and nothing is promotable.
	leaseHeld availability = iota
	// leaseAbsent is a file nobody has written yet: the machine's first start.
	leaseAbsent
	// leaseReleased is a grant its owner gave up cleanly.
	leaseReleased
	// leaseLapsed is a grant that expired without a release: its owner stopped
	// renewing without saying goodbye.
	leaseLapsed
)

// observe reports the lease's current availability and which role's grant the
// record carries. It is what a Passive instance reads each tick to evaluate
// promotion.
func (l *Lease) observe(now time.Time) (availability, InstanceRole, error) {
	cur, exists, err := readRecord(l.cfg.File)
	if err != nil {
		return leaseHeld, "", err
	}
	switch {
	case !exists:
		return leaseAbsent, "", nil
	// A release is the owner's explicit statement and outranks expiry: a released
	// grant that has also expired is still a handover, not a silent death.
	case cur.Released:
		return leaseReleased, InstanceRole(cur.OwnerRole), nil
	case cur.expired(now):
		return leaseLapsed, InstanceRole(cur.OwnerRole), nil
	default:
		return leaseHeld, InstanceRole(cur.OwnerRole), nil
	}
}

// ownedExpiry is the expiry of this instance's current grant, used by the renewal
// loop to decide when it must step down.
func (l *Lease) ownedExpiry() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Unix(0, l.owned.ExpiresUnixNano)
}

func (l *Lease) setOwned(r record) {
	l.mu.Lock()
	l.owned = r
	l.held = true
	l.mu.Unlock()
}

func (l *Lease) markLost() {
	l.mu.Lock()
	l.held = false
	l.mu.Unlock()
}

// Held reports whether this instance currently holds Primary Ownership. A nil
// Lease is Active by construction and always holds it.
func (l *Lease) Held() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held
}

// Role returns the instance role this lease handle acts for.
func (l *Lease) Role() InstanceRole {
	if l == nil {
		return RolePrimary
	}
	return l.role
}

// File returns the lease file path, empty for a nil Lease.
func (l *Lease) File() string {
	if l == nil {
		return ""
	}
	return l.cfg.File
}

// LeaseView is what an instance reports about its ownership to the health API.
type LeaseView struct {
	// Held reports whether this instance holds Primary Ownership now.
	Held bool
	// Expiry is when this instance's grant lapses, the zero time when it holds
	// none or is Active by construction.
	Expiry time.Time
}

// View reports this instance's current ownership for the /health/ha endpoint. A
// nil Lease is Active by construction: it holds ownership with no expiry, because
// there is no lease to grant or expire.
func (l *Lease) View() LeaseView {
	if l == nil {
		return LeaseView{Held: true}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held {
		return LeaseView{}
	}
	return LeaseView{Held: true, Expiry: time.Unix(0, l.owned.ExpiresUnixNano)}
}

// readRecord reads the lease file. It reports exists=false for an absent file,
// which is the state before the machine's first acquisition.
func readRecord(path string) (record, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return record{}, false, nil
	}
	if err != nil {
		return record{}, false, fmt.Errorf("redundancy: read lease %s: %w", path, err)
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return record{}, false, fmt.Errorf("redundancy: decode lease %s: %w", path, err)
	}
	return r, true, nil
}

// writeRecord writes the lease record atomically, so a concurrent reader on the
// machine's other instance never sees a torn grant.
func writeRecord(path string, r record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("redundancy: encode lease: %w", err)
	}
	if err := atomicfile.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("redundancy: write lease %s: %w", path, err)
	}
	return nil
}
