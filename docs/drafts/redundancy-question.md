give me recommendations how to achieve exactly one active winservice with primary/standby winservices
two identical binaries running as two winservices on two different ports, only one is accepting local requests
second one is passive/warm  
Both services on the same OS instance, separate ports;


- Use health checks only for promotion decisions.
- Do not use health checks as the primary ownership mechanism.
- Lease instead of lock: Use a lease mechanism to determine which service is active. The active service holds the lease, and the passive service checks for the lease before accepting requests. If the lease expires or is released, the passive service can acquire it and become active.
Each service periodically:

Checks whether it owns leadership. (Leases are time-bound and must be renewed.)
Checks peer health.
Decides whether promotion is allowed.
Fencing token: Include monotonic counter in lease; standby only accepts higher counter

Only lease owner is ACTIVE.2 3Lease renewed every 5 seconds.4 5Standby checks:6    Lease expired?7    Peer unhealthy?8 9If both true:10    Acquire lease.11    Become ACTIVE.