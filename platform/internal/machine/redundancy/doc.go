// Package redundancy manages local Primary Ownership for one machine.
//
// Primary and Standby roles are fixed. Active state comes from a finite,
// renewable lease. A Standby may acquire a lapsed lease only when its peer is
// unhealthy. Under the preferred-primary policy, an Active Standby hands
// ownership back after the Primary remains healthy for the stabilization
// interval.
//
// ManageOwnership sequences caller-supplied Passive and Active functions. It
// returns from Passive before calling Active and releases ownership only after
// Active returns. The package does not depend on site distribution.
//
// Ownership and activation transitions are machine-scoped. Process-local lease
// attempts and waiting facts are instance-scoped.
//
// See internal/machine/README.md for the machine lifecycle.
package redundancy
