# OPDL Platform API

HTTP API served by the OPDL platform runtime.

## Table of Contents

HTTP Request | Description
-------------|------------
GET [/health](#gethealth) | Report primary operational health
GET [/health/ha](#gethealthha) | Report high-availability and ownership diagnostics
GET [/health/live](#gethealthlive) | Report process liveness
GET [/health/ready](#gethealthready) | Report operational readiness
GET [/health/services](#gethealthservices) | Report this instance's view of the site's deployed services
GET [/instance](#getinstance) | Report this instance's identity and state

## GET /health

Overall health assessment, operational status, dependency status, readiness, and basic redundancy visibility.

### Responses

#### 200 Response

OK

```json
{
   "checks": {},
   "instanceId": "Primary",
   "role": "Primary",
   "runtimeState": "Active",
   "status": "Healthy",
   "uptime": "14d 07h 23m",
   "version": "0.1.0"
}
```

#### Field Definitions

- `checks` *(object)* Dependency health check results.
- `instanceId` *(string, required)* Instance identifier.
- `role` *(string, required)* Instance role: Primary or Standby.
- `runtimeState` *(string, required)* Current runtime state: Active or Passive.
- `status` *(string, required)* Overall health status: Healthy, Degraded, or Unhealthy.
- `uptime` *(string, required)* Human-readable process uptime.
- `version` *(string, required)* Platform runtime version.

## GET /health/ha

Redundancy, lease, and primary ownership diagnostics.

### Responses

#### 200 Response

OK

```json
{
   "leaseExpirationUtc": "2026-07-24T10:15:00Z",
   "leaseState": "Owned",
   "role": "Primary",
   "runtimeState": "Active"
}
```

#### Field Definitions

- `leaseExpirationUtc` *(string)* Lease expiration timestamp in UTC.
- `leaseState` *(string, required)* Lease ownership status: Owned or Unowned.
- `role` *(string, required)* Instance role: Primary or Standby.
- `runtimeState` *(string, required)* Current runtime state: Active or Passive.

## GET /health/live

Lightweight check determining whether the process and main execution loop are running.

### Responses

#### 200 Response

OK

```json
{
   "status": "Healthy"
}
```

#### Field Definitions

- `status` *(string, required)* Process liveness status.

## GET /health/ready

Check determining whether the instance is capable of serving work.

### Responses

#### 200 Response

OK

```json
{
   "status": "Healthy"
}
```

#### Field Definitions

- `status` *(string, required)* Operational readiness status.

## GET /health/services

Every service the site deploys, what each observer last found, and whether those findings are current. Answered by every instance in every state, from memory. Unhealthy, Degraded, and Unknown services are data rather than a failure to answer, so this returns 200 for every valid view.

### Responses

#### 200 Response

OK

```json
{
   "distribution": {
      "delivered": 240,
      "dropped": [
         {
            "count": 3,
            "reason": "stale"
         }
      ],
      "publishFailed": 0,
      "published": 120,
      "rejected": [
         {
            "count": 3,
            "reason": "stale"
         }
      ],
      "state": "Connected",
      "superseded": 0
   },
   "environment": "production",
   "generatedAtUtc": "2026-07-28T12:00:00Z",
   "machine": "local-server",
   "project": "customer-a",
   "role": "primary",
   "services": [
      {
         "expectedObservers": [
            "primary",
            "standby"
         ],
         "machine": "local-server",
         "machineProfile": "local-server",
         "missingObservers": [
            "GyAVmNkB33"
         ],
         "observations": [
            {
               "ageMs": 1200,
               "checkedAtUtc": "2026-07-28T11:59:58Z",
               "consecutiveFailures": 0,
               "error": "An error occurred",
               "latencyMs": 4,
               "observerRole": "primary",
               "receivedAtUtc": "2026-07-28T11:59:58Z",
               "stale": false,
               "status": "Healthy"
            }
         ],
         "service": "alarm-service",
         "serviceRole": "master",
         "staleObservers": [
            "ionwj2qrsh"
         ],
         "status": "Healthy"
      }
   ],
   "site": "north",
   "summary": {
      "degraded": 0,
      "healthy": 2,
      "unhealthy": 1,
      "unknown": 1
   }
}
```

#### Field Definitions

- `distribution` *(ServiceHealthDistribution, required)*
- `environment` *(string, required)* Environment this view covers.
- `generatedAtUtc` *(string, required)* When this snapshot was taken, in UTC.
- `machine` *(string, required)* Machine of the instance that produced this view.
- `project` *(string, required)* Project this view covers.
- `role` *(string, required)* Fixed instance role that produced this view: primary or standby.
- `services` *(array of ServiceHealthUnit, required)* Every service at this site, in inventory order.
- `site` *(string, required)* Site this view covers.
- `summary` *(ServiceHealthSummary, required)*

**ServiceHealthDistribution**
- `delivered` *(integer, required)*: Messages received on the health subject.
- `dropped` *(array of ServiceHealthCount, required)*: Decoded reports the view did not apply, by reason.
- `publishFailed` *(integer, required)*: Observations the connection refused.
- `published` *(integer, required)*: Observations from this instance that reached the connection.
- `rejected` *(array of ServiceHealthCount, required)*: Messages rejected before decoding, by reason.
- `state` *(string, required)*: Whether expected remote observations are arriving: Connected, Partial, Isolated, or Local.
- `superseded` *(integer, required)*: Observations replaced by a newer one before being sent.

**ServiceHealthCount**
- `count` *(integer, required)*: How many times it has happened.
- `reason` *(string, required)*: Why the message was not applied.

**ServiceHealthCount**
- `count` *(integer, required)*: How many times it has happened.
- `reason` *(string, required)*: Why the message was not applied.

**ServiceHealthUnit**
- `expectedObservers` *(string array, required)*: Platform instance roles expected to report on this service.
- `machine` *(string, required)*: Machine hosting this service.
- `machineProfile` *(string, required)*: Purpose of the hosting machine.
- `missingObservers` *(string array, required)*: Expected observers that have never reported.
- `observations` *(array of ServiceHealthObservation, required)*: Last report from each observer, ordered by observer role.
- `service` *(string, required)*: Service name.
- `serviceRole` *(string, required)*: Part this copy plays: master or slave.
- `staleObservers` *(string array, required)*: Expected observers whose last report has expired.
- `status` *(string, required)*: Reduced service status: Healthy, Unhealthy, Degraded, or Unknown.

**ServiceHealthObservation**
- `ageMs` *(integer, required)*: Milliseconds since this report arrived, on the answering instance's clock.
- `checkedAtUtc` *(string, required)*: When the observer says it probed, in UTC.
- `consecutiveFailures` *(integer, required)*: Attempts that had failed in a row when the observer reported.
- `error` *(string)*: Why the observer's probe failed, absent when it succeeded.
- `latencyMs` *(integer, required)*: Milliseconds the observer's probe took.
- `observerRole` *(string, required)*: Platform instance role that reported: primary or standby.
- `receivedAtUtc` *(string, required)*: When this instance received the report, in UTC.
- `stale` *(boolean, required)*: Whether this report has passed the service's freshness bound.
- `status` *(string, required)*: Status this observer reported: Healthy, Unhealthy, or Unknown.

**ServiceHealthSummary**
- `degraded` *(integer, required)*: Services whose current observers disagree.
- `healthy` *(integer, required)*: Services every current observer found well.
- `unhealthy` *(integer, required)*: Services every current observer found failing.
- `unknown` *(integer, required)*: Services nothing current says anything about.

## GET /instance

Answered by every instance in every state, including a Passive one that refuses every domain operation.

### Responses

#### 200 Response

OK

```json
{
   "address": "127.0.0.1:8080",
   "machine": "local-server",
   "peer_address": "127.0.0.1:8081",
   "role": "primary",
   "state": "active"
}
```

#### Field Definitions

- `address` *(string, required)* This instance's own loopback API address.
- `machine` *(string, required)* Descriptor machine this instance runs on.
- `peer_address` *(string)* The machine's other instance's API address, if one is deployed.
- `role` *(string, required)* Fixed build-time instance role: primary or standby.
- `state` *(string, required)* Current runtime state: active or passive.

