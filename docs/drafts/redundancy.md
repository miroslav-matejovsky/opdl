Terminology

Availability Pattern:
- Fixed-Role Primary/Standby

Policy:
- Preferred Primary

Roles:
- Primary Instance
- Standby Instance

Runtime States:
- Active
- Passive

Ownership:
- Primary Ownership

Ownership Operations:
- Ownership Acquisition
- Ownership Validation
- Ownership Renewal
- Ownership Release
- Ownership Transfer

Ownership Mechanism:
- Lease-Based Primary Ownership

Optional Ownership Protection:
- Fencing Token
- Ownership Generation
- Ownership Epoch

Transitions:
- Failover
- Failback

Avoid Using:

- Leader
- Follower
- Election
- Leadership
- Consensus
- Quorum
- Distributed Lock
- Leadership Token

Architecture Overview

The platform uses a Fixed-Role Primary/Standby availability architecture operating under a Preferred Primary policy.

Two predefined platform processes run on every machine:

- Primary Instance
- Standby Instance

These roles are static, intentional, and operationally well-known. This is not a leader-election architecture, distributed consensus system, quorum-based system, or peer-to-peer ownership model. The instances are not equal participants competing for ownership.

The Primary Instance is the preferred operational instance and should be Active whenever it is healthy and owns Primary Ownership. The Standby Instance exists to provide local high availability, failure recovery, maintenance flexibility, and upgrade continuity.

Roles and runtime state are separate concepts.

Roles:
- Primary Instance
- Standby Instance

Runtime States:
- Active
- Passive

Under normal conditions:

- Primary Instance → Active
- Standby Instance → Passive

Primary Ownership

Primary Ownership is determined through a lease mechanism.

Ownership Rules:

- Lease Owner → Active Instance
- Non-Owner → Passive Instance
- Only one valid lease owner may exist at a time
- Active state requires valid lease ownership
- Lease ownership is the authoritative source of ownership state

Ownership Characteristics:

- Lease ownership determines runtime state
- Lease ownership must be periodically renewed
- Expired leases immediately lose ownership authority
- Ownership decisions are based exclusively on lease state
- Health status does not confer ownership
- Inter-process communication does not confer ownership

Health Check Policy

Health checks are used only for promotion decisions.

Health checks are not the ownership mechanism.

Health status is exposed through standard health endpoints.

Health checks are used to determine:

- Whether failover is permitted
- Whether failback is permitted
- Whether ownership transfer is allowed

Health checks are not used to:

- Establish ownership
- Maintain ownership
- Validate ownership
- Override ownership

Promotion Logic

Each instance periodically:

- Validates lease ownership
- Renews the lease if currently owner
- Checks peer health through the health endpoint
- Evaluates promotion eligibility

Active Instance:

- Owns the lease
- Renews the lease periodically
- Operates in Active state
- Serves platform responsibilities

Passive Instance:

- Does not own the lease
- Operates in Passive state
- Monitors lease state
- Monitors peer health
- Evaluates failover conditions

Failover Conditions

Promotion is allowed only when:

- The lease has expired or has been released
- The peer is unhealthy

If both conditions are true:

- Ownership Acquisition occurs
- The instance becomes Active
- Lease renewal begins

Failover Sequence

- Primary Instance becomes unavailable
- Lease renewal stops
- Lease expires
- Standby Instance validates promotion conditions
- Standby Instance acquires Primary Ownership
- Standby Instance transitions to Active

Failback Sequence

- Primary Instance becomes healthy again
- Failback policy determines whether ownership transfer is allowed
- Ownership Transfer occurs
- Primary Instance acquires Primary Ownership
- Primary Instance transitions to Active
- Standby Instance transitions to Passive

Lease Configuration

Recommended lease behavior:

- Lease duration must be finite
- Lease renewal must occur periodically
- Ownership is valid only while the lease remains valid
- Ownership automatically expires when renewal stops

Example:

- Lease Duration: 15 seconds
- Lease Renewal Interval: 5 seconds

Optional Fencing Token Protection

The platform may optionally implement a fencing token to protect against stale ownership.

Purpose:

- Prevent a previous owner from continuing to perform ownership-sensitive operations after ownership has transferred

Fencing Rules:

- Each successful ownership acquisition generates a monotonically increasing value
- The value is known as a Fencing Token, Ownership Generation, or Ownership Epoch
- Every ownership-sensitive operation carries the current token
- Consumers accept only the highest token observed
- Operations associated with older tokens are rejected

Example:

- Ownership Acquisition #17 → Token 17
- Ownership Acquisition #18 → Token 18

Valid:
- Token 18

Rejected:
- Token 17

Fencing is strongly recommended when ownership controls:

- Database updates
- Shared storage
- Shared queues
- External systems
- Stateful operations
- Irreversible business actions

Fencing is optional when ownership affects only local in-memory activities.

Split-Brain Protection

The primary split-brain protection mechanism is lease ownership.

Protection Rules:

- Only the lease owner may operate as Active
- Non-owners must remain Passive
- Ownership is validated before Active operations are performed
- Lease loss requires immediate transition to Passive state
- Active operation without valid lease ownership is prohibited

If fencing tokens are enabled:

- Stale owners are additionally prevented from modifying protected resources even after delayed recovery

Runtime Isolation

Each instance operates as an independent runtime with:

- Independent process
- Independent memory space
- Independent threads
- Independent lifecycle
- Independent logs
- Independent configuration context
- Independent network ports

Communication Principles

- Service-to-platform commands, queries, configuration, and administration use the existing local API
- Health visibility uses standard health endpoints
- Ownership decisions must always be based on lease ownership
- Ownership-sensitive operations may optionally enforce fencing token validation
- Communication state must never be used as ownership authority

Architecture Summary

The architecture is a Fixed-Role Primary/Standby platform operating under a Preferred Primary policy.

Primary Ownership is determined through lease-based ownership.

Health checks are used only to decide whether promotion is allowed.

Lease ownership is the authoritative source of ownership state.

Active and Passive runtime states are derived exclusively from lease ownership.

Optional fencing-token protection may be used to prevent stale-owner operations after ownership transfer.

The architecture provides local high availability through controlled failover and failback while maintaining a single authoritative owner at all times.