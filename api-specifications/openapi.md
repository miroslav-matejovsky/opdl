# OPDL Platform API

HTTP API served by the OPDL platform runtime.

## Table of Contents

HTTP Request | Description
-------------|------------
GET [/health](#gethealth) | Report primary operational health
GET [/health/ha](#gethealthha) | Report high-availability and ownership diagnostics
GET [/health/live](#gethealthlive) | Report process liveness
GET [/health/ready](#gethealthready) | Report operational readiness
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

