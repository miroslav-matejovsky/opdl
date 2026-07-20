# Requirements

## Orchestration Layer

The orchestration layer provides the shared runtime capabilities required for coordinating distributed services.
It comprises **identity**, **service registry and discovery**, **presence and health**, a **messaging fabric** (publish/subscribe and request/response),
**command handling**, **redundancy and leader election**,
and **connectivity lifecycle management**.

The orchestration layer defines behavioral requirements and interfaces.
It does not prescribe a specific product, transport protocol, broker technology, or deployment topology.

***

### 1. Identity and Addressing

* **OR-1 (MUST)** Each service instance MUST have a stable **logical identity** consisting of a logical service name and an instance identifier.
* **OR-2 (MUST)** Identity MUST NOT depend on centrally allocated numeric identifiers.
* **OR-3 (MUST)** A service MUST be able to declare **capability metadata**, including:
  * interfaces it provides,
  * data or events it publishes,
  * data or events it consumes,
  * requests or commands it handles.
* **OR-4 (SHOULD)** Identity SHOULD include human-readable metadata such as display name, version, owner, domain, or deployment environment.
* **OR-5 (MUST)** The platform MUST support mapping between logical identities and any legacy identification schemes required during migration from existing systems.

***

### 2. Service Registry and Discovery

* **OR-6 (MUST)** The platform MUST provide a **service registry** where service instances register their identity and capabilities when starting.
* **OR-7 (MUST)** Registered entries MUST be removed or expired when an instance shuts down or becomes unavailable.
* **OR-8 (MUST)** Services MUST be able to discover other services by logical name, capability, or declared interface.
* **OR-9 (MUST)** Service discovery MUST NOT require consumers to know physical network addresses.
* **OR-10 (SHOULD)** Discovery SHOULD be dynamic, allowing consumers to receive notifications when relevant services appear, disappear, or change state.
* **OR-11 (MUST)** The registry MUST NOT be a single runtime dependency for already-established communication paths; transient registry outages SHOULD NOT interrupt ongoing communication between discovered services.

***

### 3. Presence and Health

* **OR-12 (MUST)** The platform MUST track the liveness of registered service instances through a configurable heartbeat or keep-alive mechanism.
* **OR-13 (MUST)** Heartbeat intervals and timeout thresholds MUST be configurable.
* **OR-14 (MUST)** Presence and health state changes MUST be observable by interested services and operational tooling.
* **OR-15 (SHOULD)** The platform SHOULD distinguish between:
  * a service being reachable, and
  * a service being ready to perform work.
* **OR-16 (SHOULD)** Readiness status SHOULD be independently reportable and discoverable.

***

### 4. Messaging Fabric (Publish/Subscribe)

* **OR-17 (MUST)** The platform MUST provide a **publish/subscribe messaging capability** based on named topics or channels.
* **OR-18 (MUST)** Publishers MUST be able to publish messages without knowledge of subscribers.
* **OR-19 (MUST)** Subscribers MUST be able to subscribe to topics independently of publishers.
* **OR-20 (MUST)** Topic subscriptions MUST support content-based or attribute-based filtering.
* **OR-21 (MUST)** The messaging fabric MUST support one-to-many message distribution so that publishers emit messages once regardless of subscriber count.
* **OR-22 (SHOULD)** The platform SHOULD define and document delivery semantics for each message category, such as at-most-once, at-least-once, or exactly-once delivery.
* **OR-23 (MUST)** Delivery guarantees provided by the platform MUST be explicitly documented and visible to developers.
* **OR-24 (SHOULD)** The platform SHOULD support configurable ordering guarantees at topic, partition, stream, or key level where required.
* **OR-25 (SHOULD)** The platform SHOULD provide explicit backpressure handling, including configurable overflow behavior such as blocking, buffering, dropping, throttling, or disconnecting producers or consumers.
* **OR-26 (MUST)** Overflow and loss-handling behavior MUST be defined and observable.

***

### 5. Request/Response and Command Handling

* **OR-27 (MUST)** The platform MUST provide a request/response communication capability.
* **OR-28 (MUST)** Request and response exchanges MUST support correlation identifiers.
* **OR-29 (MUST)** Requests MUST be able to carry caller context metadata, including service identity, user identity, and additional audit information where available.
* **OR-30 (MUST)** Responses MUST include structured status information containing:
  * success or failure indication,
  * machine-readable status code,
  * human-readable description.
* **OR-31 (MUST)** Domain-specific status codes and error models MUST remain owned by the services implementing the relevant business functionality.
* **OR-32 (SHOULD)** Requests SHOULD support configurable timeout behavior.
* **OR-33 (SHOULD)** Timeout MUST be represented as a first-class outcome distinct from application-level failure.
* **OR-34 (MAY)** The platform MAY support routing requests to an active instance of a logical service when redundancy management is enabled.

***

### 6. Redundancy and Leader Election

* **OR-35 (MUST)** Redundancy management MUST be provided as a platform capability.
* **OR-36 (MUST)** Services MUST be able to declare operational constraints such as single-active execution.
* **OR-37 (MUST)** The platform MUST coordinate active-instance assignment for services requiring single-active execution.
* **OR-38 (MUST)** The platform MUST provide leader-election capabilities for groups of instances participating in coordinated failover.
* **OR-39 (MUST)** Interested services and operational tooling MUST be notified when leadership or active-instance ownership changes.
* **OR-40 (SHOULD)** The platform SHOULD support autonomous site-local operation during loss of connectivity to external or central infrastructure.
* **OR-41 (SHOULD)** Leader-election behavior, failover timing, retry policies, and re-election mechanisms SHOULD be configurable.
* **OR-42 (SHOULD)** Redundancy state and failover events SHOULD be observable through operational tooling and monitoring interfaces.

***

### 7. Connectivity and Lifecycle

* **OR-43 (MUST)** The platform MUST provide a unified connectivity and lifecycle model for all communication patterns.
* **OR-44 (MUST)** The lifecycle model MUST include:
  * connection establishment,
  * authentication,
  * session maintenance,
  * keep-alive handling,
  * reconnection,
  * shutdown.
* **OR-45 (MUST)** Reconnection MUST be automatic and transparent to application logic wherever technically feasible.
* **OR-46 (MUST)** Reconnection behavior MUST support configurable retry and backoff policies.
* **OR-47 (MUST)** The platform MUST expose communication state through a standard interface, including at least:
  * connected,
  * degraded,
  * disconnected.
* **OR-48 (SHOULD)** The lifecycle model SHOULD provide graceful shutdown procedures including:
  * draining in-flight work,
  * releasing platform resources,
  * deregistering services,
  * relinquishing leadership roles.
* **OR-49 (MUST)** Lifecycle transitions MUST be observable by services and operational tooling.

***

### 8. Explicitly Out of Scope

The following concerns are outside the scope of the orchestration layer:

* Business-domain message definitions.
* Domain workflows and processing logic.
* Service-specific commands and request contracts.
* Service-specific status code catalogs.
* Authorization policies and business permissions.
* Physical deployment topology.
* Messaging broker implementation choices.
* Network transport protocols.
* Vendor products and platform technologies.

The orchestration layer specifies **behavioral capabilities and contracts**, not implementation technologies.

---

## Configuration Layer

The configuration layer provides **centralized, versioned, push-based** configuration delivered on a dedicated channel, with consistent support for dynamic reconfiguration.

It addresses common challenges associated with distributed configuration management, including excessive configuration traffic, tight coupling between services and configuration providers, and unclear configuration lifecycle management.

### 1. Configuration Model

* **CF-1 (MUST)** The platform MUST provide a **central configuration store** that serves as the authoritative source for service and system configuration, including configuration objects, parameters, relationships, and static reference data.
* **CF-2 (MUST)** Configuration MUST be organized in a **scoped and hierarchical** manner (for example, global, environment, deployment, service type, and instance scopes) so values can be defined broadly and overridden at more specific levels.
* **CF-3 (MUST)** Every configuration change MUST produce a **new version** (or revision) so that consumers and operators can determine exactly which configuration is in effect.
* **CF-4 (SHOULD)** Configuration schemas SHOULD be defined and validated through a contract or schema registry (see [Cross-Cutting Requirements](07-cross-cutting-requirements.md)) so invalid configuration is rejected before distribution.

### 2. Delivery

* **CF-5 (MUST)** On startup, a service MUST be able to **retrieve its complete effective configuration** for its scope in a single, well-defined operation, avoiding multiple configuration queries and round trips.
* **CF-6 (MUST)** Configuration changes MUST be **pushed** to affected services as the **actual changed values** rather than as invalidation notifications that require re-fetching configuration data.
* **CF-7 (MUST)** Configuration delivery MUST use a **dedicated channel or topic**, conceptually separate from control-plane commands and business/data-plane communication, even if they share the same underlying transport.
* **CF-8 (SHOULD)** Delivery SHOULD be **targeted**, ensuring that services receive only the configuration relevant to their scope, role, and identity.

### 3. Dynamic Reconfiguration

* **CF-9 (MUST)** The platform and SDK MUST support **dynamic application** of configuration changes at runtime for parameters that support hot reconfiguration, with a clear notification mechanism that provides the changed value and its new state.
* **CF-10 (SHOULD)** Where a change cannot be applied dynamically, the platform SHOULD make the **restart or reload requirement explicit** (declared per configuration item) rather than relying on implicit behavior.
* **CF-11 (SHOULD)** The SDK SHOULD provide a consistent pattern for services to subscribe to configuration change events, reducing the need for service-specific configuration handling implementations.

### 4. Consistency and Safety

* **CF-12 (MUST)** Configuration reads MUST be **consistent**. A service MUST be able to obtain a coherent snapshot of a specific configuration version and MUST NOT observe a partially updated configuration state.
* **CF-13 (SHOULD)** The platform SHOULD support **atomic multi-value updates** so related configuration items change together and consumers never observe an inconsistent intermediate state.
* **CF-14 (SHOULD)** The configuration store SHOULD retain **change history** and support **rollback** to previous versions for operational recovery and troubleshooting.
* **CF-15 (SHOULD)** Configuration write operations SHOULD be **authorized and audited**, recording who made a change, what changed, and when the change occurred, using the platform's security and identity mechanisms.

### 5. Bootstrap Configuration

* **CF-16 (MUST)** The minimal settings required for a service to **connect to the platform** (for example identity, platform endpoints, and credentials) MUST be available before platform-managed configuration can be retrieved. This bootstrap configuration MUST be clearly separated from centrally managed configuration and kept intentionally small.
* **CF-17 (SHOULD)** Bootstrap configuration SHOULD support environment-based and automated provisioning mechanisms so service instances can be deployed without manual modification of local configuration files.

### 6. Migration Considerations

* **CF-18 (MUST)** During migration, the configuration layer MUST support **coexistence and synchronization** between legacy and modernized configuration mechanisms so that all participating services observe a consistent configuration state.
* **CF-19 (SHOULD)** The configuration layer SHOULD provide a migration path for importing existing configuration data, including configuration objects, parameters, relationships, and static reference data, into the new configuration store.

### 7. Explicitly Not Carried Forward

* Legacy configuration protocols, message formats, and API generations are **not** reproduced. The platform exposes a single, versioned configuration contract.
* Invalidation-based update mechanisms that require consumers to re-query configuration data are replaced by direct delivery of configuration changes (CF-6).
* Service-specific configuration distribution patterns are replaced by a common platform configuration model and delivery mechanism.

---

## Data Layer

The data layer provides scalable distribution of live domain object data (entities, observations, events, alarms, zones, and similar domain concepts) using a **pub/sub** fabric, with **snapshot + delta** semantics and **schema-driven contracts** as first-class platform guarantees rather than application-specific conventions.

Addresses problems:

* Point-to-point data distribution does not scale.
* Wire formats become coupled to in-memory object layouts.
* Subscription, delta, resynchronisation, and liveness logic are repeatedly implemented by individual services.
* Multiple transports duplicate connectivity and lifecycle concerns.

### 1. Distribution model

* **DL-1 (MUST)** Live object data MUST be distributed through the platform **pub/sub** fabric. Producers publish object updates to topics; the fabric handles fan-out to all subscribers. Producers MUST NOT manage direct connections or per-consumer state for each consumer.

* **DL-2 (MUST)** A producer MUST be able to publish an update **once** regardless of the number of consumers.

* **DL-3 (MUST)** Consumers MUST be able to **subscribe by object type and by attributes** (for example geographic scope, ownership domain, classification, tenant, or other metadata) so they receive only relevant data.

* **DL-4 (SHOULD)** The data layer SHOULD reuse the platform's shared connectivity, discovery, presence, and lifecycle capabilities rather than defining its own.

### 2. Snapshot and delta semantics

* **DL-5 (MUST)** The platform MUST provide **snapshot + delta** delivery: a new or re-subscribing consumer MUST receive a consistent **initial snapshot** of matching objects, followed by **incremental deltas** (adds, updates, removals).

* **DL-6 (MUST)** Removal of objects MUST be an explicit, delivered event so consumers can prune local state deterministically.

* **DL-7 (MUST)** The platform MUST provide a **resynchronisation** mechanism: if a consumer detects it is out of sync (gap, corruption, or reconnect), it MUST be able to request a fresh snapshot. The correctness guarantee MUST be provided by the platform rather than implemented independently by each consumer.

* **DL-8 (SHOULD)** Deltas SHOULD support **partial (field-level) updates** so that only changed fields are transmitted. Partial update behaviour SHOULD be expressed through schema definitions rather than object memory layouts.

* **DL-9 (SHOULD)** The platform SHOULD define per-object **ordering** so consumers apply updates for a given object in the order they were produced.

### 3. Liveness and staleness

* **DL-10 (MUST)** The platform MUST signal when a data source becomes **unavailable** so consumers can clear, remove, or mark the corresponding data as stale. This behaviour MUST be driven by platform-level presence and lifecycle information rather than application-specific timeout heuristics.

* **DL-11 (SHOULD)** Staleness signalling SHOULD distinguish **"source unavailable"** from **"no changes observed"** so consumers do not discard valid data during quiet periods.

### 4. On-demand queries

* **DL-12 (MUST)** The platform MUST support an **on-demand request/response** mechanism that allows consumers to retrieve a current set of objects without establishing a continuous subscription.

* **DL-13 (SHOULD)** On-demand queries SHOULD support the same type and attribute filtering capabilities as subscriptions (DL-3).

### 5. Contracts and serialisation

* **DL-14 (MUST)** Object data contracts MUST be defined as **versioned schemas** in a contract/schema registry (see Cross-Cutting Requirements), independent of any language's in-memory representation.

* **DL-15 (MUST)** The serialisation format MUST support **schema evolution** (for example adding fields, deprecating fields, or introducing optional fields) without breaking existing producers or consumers.

* **DL-16 (MUST)** Object identity MUST be expressed in a structured and documented manner (for example object type and object instance identifier), including support for type-level subscription patterns or wildcards where required.

* **DL-17 (SHOULD)** The format SHOULD be efficient enough for high-frequency streaming workloads. Performance targets are defined in the Non-Functional Requirements.

* **DL-18 (SHOULD)** Contracts SHOULD be **language-neutral** so producers, consumers, and integrations implemented in different technologies can interoperate.

### 6. Domain object model decoupling

* **DL-19 (MUST)** The platform MUST NOT require a service's internal domain model to be identical to the wire contract. Services MUST be able to map between internal domain objects and published schemas.

* **DL-20 (SHOULD)** The SDK SHOULD provide reusable facilities for maintaining a local, queryable materialised view of subscribed object sets so individual services do not need to re-implement snapshot acquisition, delta application, and state management.

### 7. Recording, playback, and replay

* **DL-21 (SHOULD)** The data layer SHOULD allow live, recorded, and replayed data to flow through the **same subscription contracts**, enabling consumers to switch between live and historical sources without code changes.

* **DL-22 (MAY)** The platform MAY support **durable or retained streams** so late subscribers, recorders, or replay services can obtain recent history directly from the fabric. Durability remains an open architectural decision.

### 8. Explicitly not carried over

* Fixed-interval delivery modes are **not required**. Such behaviour MAY be introduced later if justified by concrete requirements.

* Manual per-consumer subscription management by producers is intentionally excluded.

* Consumer-implemented integrity verification and resubscription patterns are replaced by platform-provided integrity guarantees and resynchronisation capabilities (DL-7).