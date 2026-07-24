Architecture Overview

The platform uses a Fixed-Role Primary/Standby availability architecture operating under a Preferred Primary policy.

Two predefined platform processes run on every machine:

- Primary Instance
- Standby Instance

These roles are static, intentional, and operationally well-known. This is not a leader-election architecture, distributed consensus system, quorum-based system, or peer-to-peer ownership model. The instances are not equal participants competing for ownership.

The Primary Instance is the preferred operational instance and should be Active whenever it is healthy and eligible to own Primary Ownership. The Standby Instance exists to provide local high availability, failure recovery, maintenance flexibility, and upgrade continuity.

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

Primary Ownership is determined through a Windows Named Mutex.

Ownership Terminology:

- Primary Ownership
- Ownership Acquisition
- Ownership Validation
- Ownership Release
- Ownership Transfer

Ownership Rules:

- Mutex Owner → Active Instance
- Non-Owner → Passive Instance
- Only one mutex owner may exist at a time

Ownership Characteristics:

- Windows Named Mutex is the authoritative ownership mechanism
- Health checks are not the ownership mechanism
- Ownership decisions are based exclusively on mutex ownership
- Runtime state is derived from ownership state
- Ownership decisions are never based solely on peer communication state

Health Check Policy:

- Use health checks only for promotion decisions
- Do not use health checks as the primary ownership mechanism
- Health checks determine whether ownership acquisition or ownership transfer is permitted
- Health checks do not establish ownership
- Health status is exposed through standard health endpoints

Promotion Logic

Each instance periodically:

- Validates mutex ownership
- Checks peer health through the health endpoint
- Evaluates promotion eligibility

Active Instance:

- Owns Primary Ownership through the Windows Named Mutex
- Operates in Active state
- Serves platform responsibilities

Passive Instance:

- Does not own Primary Ownership
- Operates in Passive state
- Monitors peer health
- Evaluates failover conditions

Failover Conditions

The Passive Instance may acquire Primary Ownership only when:

- Primary Ownership is no longer held
- The peer is determined to be unhealthy

If both conditions are satisfied:

- Ownership Acquisition occurs
- The instance becomes Active

Failover Sequence:

- Primary Instance becomes unavailable
- Windows releases the mutex
- Standby Instance detects ownership availability
- Standby Instance validates promotion conditions
- Standby Instance acquires Primary Ownership
- Standby Instance transitions to Active

Failback Sequence:

- Primary Instance becomes healthy again
- The configured failback policy determines whether ownership transfer is allowed
- Ownership Transfer occurs
- Primary Instance acquires Primary Ownership
- Primary Instance transitions to Active
- Standby Instance transitions to Passive

Split-Brain Protection

The Windows Named Mutex provides exclusive ownership enforcement.

Protection Rules:

- Only the mutex owner may operate as Active
- Non-owners must remain Passive
- Ownership is validated before Active operations are performed
- Ownership loss requires immediate transition to Passive state
- Active state without valid ownership is prohibited

Each instance operates as an independent runtime with:

- Independent process
- Independent memory space
- Independent threads
- Independent lifecycle
- Independent logs
- Independent configuration context
- Independent network ports

Communication Principles:

- Service-to-platform commands, queries, configuration, and health operations use the existing local API
- Health visibility uses standard health endpoints
- Ownership decisions must always be based on Windows Named Mutex ownership
- Inter-process communication must never be used as an ownership authority

Preferred Terminology

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

Transitions:
- Failover
- Failback

Ownership Mechanism:
- Windows Named Mutex

Avoid Using:

- Leader
- Follower
- Election
- Leadership
- Consensus
- Quorum
- Distributed Lock
- Leadership Token

The architecture should be described as a Fixed-Role Primary/Standby platform operating under a Preferred Primary policy, with Windows Named Mutex providing authoritative Primary Ownership, health-gated promotion decisions, split-brain protection, and local high availability.