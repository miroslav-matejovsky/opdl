# OPDL Platform API

HTTP API served by the OPDL platform runtime.

## Table of Contents

HTTP Request | Description
-------------|------------
GET [/instance](#getinstance) | Report this instance's identity and state
GET [/registrations](#getregistrations) | List registration proposals
POST [/registrations](#postregistrations) | Propose a unit registration
GET [/registrations/conflicts](#getregistrationsconflicts) | List resolved registration conflicts
GET [/registrations/{proposal_id}](#getregistrationsproposalid) | Get a registration proposal's status

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

## GET /registrations

List registration proposals

### Responses

#### 200 Response

OK

```json
[
   {
      "ip": "10.0.0.11",
      "machine": "site-1",
      "platform_instances": [
         {
            "ip": "10.0.0.21",
            "machine": "node-a",
            "reason": "registration_key_conflict",
            "status": "accepted"
         }
      ],
      "proposal_id": "c1a2b3",
      "reason": "registration_key_conflict",
      "role": "Master",
      "status": "accepted",
      "unit_id": 42,
      "unit_type": 7,
      "unit_type_name_advertised": "Billing"
   }
]
```

#### Field Definitions

## POST /registrations

Propose a unit registration

### Request

```json
{
   "role": "Master",
   "unit_id": 42,
   "unit_type": 7,
   "unit_type_name_advertised": "Billing"
}
```

#### Field Definitions

- `role` *(string)* Optional Master or Slave role advertised by the unit.
- `unit_id` *(integer, required)* Unit identifier, 0 through 65535.
- `unit_type` *(integer, required)* Unit type identifier, 0 through 255.
- `unit_type_name_advertised` *(string, required)* Advertised unit type name. Required and non-blank.

### Responses

#### 202 Response

Accepted

```json
{
   "proposal_id": "c1a2b3",
   "sequence": 12
}
```

#### Field Definitions

- `proposal_id` *(string, required)* Stable proposal identity and status key.
- `sequence` *(integer, required)* Proposal position in the site journal.

#### 400 Response

Bad Request

#### 422 Response

Unprocessable Entity

#### 500 Response

Internal Server Error

#### 503 Response

Service Unavailable

## GET /registrations/conflicts

List resolved registration conflicts

### Responses

#### 200 Response

OK

```json
[
   {
      "losers": [
         {
            "ip": "10.0.0.11",
            "machine": "site-1",
            "platform_instances": [],
            "proposal_id": "c1a2b3",
            "reason": "registration_key_conflict",
            "role": "Master",
            "status": "accepted",
            "unit_id": 42,
            "unit_type": 7,
            "unit_type_name_advertised": "Billing"
         }
      ],
      "resolution_status": "resolved",
      "unit_id": 42,
      "unit_type": 7,
      "winner": {
         "ip": "10.0.0.11",
         "machine": "site-1",
         "platform_instances": [
            {
               "ip": "10.0.0.21",
               "machine": "node-a",
               "reason": "registration_key_conflict",
               "status": "accepted"
            }
         ],
         "proposal_id": "c1a2b3",
         "reason": "registration_key_conflict",
         "role": "Master",
         "status": "accepted",
         "unit_id": 42,
         "unit_type": 7,
         "unit_type_name_advertised": "Billing"
      }
   }
]
```

#### Field Definitions

#### 500 Response

Internal Server Error

## GET /registrations/{proposal_id}

Get a registration proposal's status

#### Path Parameters

- `proposal_id` *(string, required)* Opaque proposal identifier returned by registerUnit.

### Responses

#### 200 Response

OK

```json
{
   "ip": "10.0.0.11",
   "machine": "site-1",
   "platform_instances": [
      {
         "ip": "10.0.0.21",
         "machine": "node-a",
         "reason": "registration_key_conflict",
         "status": "accepted"
      }
   ],
   "proposal_id": "c1a2b3",
   "reason": "registration_key_conflict",
   "role": "Master",
   "status": "accepted",
   "unit_id": 42,
   "unit_type": 7,
   "unit_type_name_advertised": "Billing"
}
```

#### Field Definitions

- `ip` *(string, required)* Descriptor IP where the request originated.
- `machine` *(string, required)* Descriptor machine where the request originated.
- `platform_instances` *(array of PlatformInstanceRegistrationStatus, required)* Deterministic progress view for each platform instance.
- `proposal_id` *(string, required)* Stable proposal identity and status key.
- `reason` *(string)* Optional machine-readable rejection code.
- `role` *(string)* Optional Master or Slave role advertised by the unit.
- `status` *(string, required)* Registration status: pending, accepted, or rejected.
- `unit_id` *(integer, required)* Registered unit identifier.
- `unit_type` *(integer, required)* Registered unit type identifier.
- `unit_type_name_advertised` *(string, required)* Advertised unit type name.

**PlatformInstanceRegistrationStatus**
- `ip` *(string, required)*: Platform instance's descriptor IP, empty when the deployment no longer has the machine.
- `machine` *(string, required)*: Platform instance's descriptor machine.
- `reason` *(string)*: Optional machine-readable rejection code.
- `status` *(string, required)*: Platform instance's registration status: pending, accepted, or rejected.

#### 404 Response

Not Found

#### 422 Response

Unprocessable Entity

#### 500 Response

Internal Server Error

