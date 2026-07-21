Architecture Overview

The platform uses a Fixed-Role Primary/Standby availability architecture.

Two predefined platform processes run on every machine:

- Primary Instance
- Standby Instance

These roles are static, intentional, and operationally well-known. This is not a leader-election architecture, distributed consensus system, quorum-based system, or peer-to-peer ownership model. The instances are not equal participants competing for ownership.

The platform operates under a Preferred Primary policy. The Primary Instance is the preferred operational instance and should be Active whenever it is healthy and available. The Standby Instance exists to provide local high availability, failure recovery, maintenance flexibility, and upgrade continuity.

Roles and runtime state are treated as separate concepts.

Roles:
- Primary Instance
- Standby Instance

Runtime States:
- Active
- Passive

Under normal conditions:

- Primary Instance → Active
- Standby Instance → Passive

The authoritative ownership mechanism is a Windows Named Mutex. The mutex represents Primary Ownership and serves as the single source of truth for local instance ownership.

Ownership terminology:

- Primary Ownership
- Ownership Acquisition
- Ownership Validation
- Ownership Release
- Ownership Transfer

Ownership rules:

- Mutex Owner → Active Instance
- Non-Owner → Passive Instance

The platform does not use lock files, marker files, shared filesystem state, or filesystem-based coordination mechanisms.

During a failover event:

- Primary Instance becomes unavailable
- Windows releases the mutex
- Standby Instance acquires Primary Ownership
- Standby Instance transitions to Active

During a failback event:

- Primary Instance becomes healthy again
- Ownership is transferred back according to the configured failback policy
- Primary Instance transitions to Active
- Standby Instance transitions to Passive

Each instance operates as an independent runtime with:

- Independent process
- Independent memory space
- Independent threads
- Independent lifecycle
- Independent logs
- Independent configuration context
- Independent network ports

The only shared resource between instances is the Windows Named Mutex used for Primary Ownership.

Communication principles:

- Service-to-platform commands, queries, configuration, and health operations use the existing local API.
- Platform-to-service notifications and events use Windows Named Pipes.
- Optional Primary/Standby coordination, diagnostics, or health visibility may also use Windows Named Pipes.
- Ownership decisions must always be based on Windows Named Mutex ownership and never on inter-process communication state.

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

Avoid using:

- Leader
- Follower
- Election
- Leadership
- Consensus
- Quorum
- Slot
- Fence
- Distributed Lock
- Leadership Token

The architecture should be described as a Fixed-Role Primary/Standby platform operating under a Preferred Primary policy, with Windows Named Mutex providing authoritative Primary Ownership and local high availability.