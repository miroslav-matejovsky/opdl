# OPDL Platform API

HTTP API served by the OPDL platform runtime.

## Table of Contents

HTTP Request | Description
-------------|------------
GET [/registrations](#getregistrations) | List registration proposals
POST [/registrations](#postregistrations) | Propose a unit registration
GET [/registrations/conflicts](#getregistrationsconflicts) | List resolved registration conflicts
GET [/registrations/{proposal_id}](#getregistrationsproposalid) | Get a registration proposal's status

## GET /registrations

List registration proposals

### Responses

#### 200 Response

OK

```json
[
   {
      "ip": "eUD8gKko90",
      "machine": "482c3F34Um",
      "platform_instances": [
         {
            "ip": "FktHeN2RaQ",
            "machine": "7gULxLiKZu",
            "reason": "XJPQpTiMA2",
            "status": "yIciUtpT0d"
         }
      ],
      "proposal_id": "HVdzTs36ZI",
      "reason": "fiW1BB2v2q",
      "role": "VDSucew3vh",
      "status": "20f9wdmTZC",
      "unit_id": 53793,
      "unit_type": 19,
      "unit_type_name_advertised": "IxkCT1tdxT"
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
            "ip": "weCEnXS6kp",
            "machine": "GW0Fb86PDr",
            "platform_instances": [],
            "proposal_id": "SkYoaL6s7p",
            "reason": "2uQnwvIya0",
            "role": "VNykPeMfjO",
            "status": "wnOA6qPDkh",
            "unit_id": 29601,
            "unit_type": 8,
            "unit_type_name_advertised": "aHxHvVZliQ"
         }
      ],
      "resolution_status": "Xbpg3uoeTC",
      "unit_id": 15516,
      "unit_type": 109,
      "winner": {
         "ip": "gO0brDrEOx",
         "machine": "uQnQtxLeaT",
         "platform_instances": [
            {
               "ip": "Vq9F6FVmub",
               "machine": "jfAr37eclu",
               "reason": "pXTukPhiIq",
               "status": "PDnQEKeguG"
            }
         ],
         "proposal_id": "gWIFHhEiFS",
         "reason": "oNeyOUnnR5",
         "role": "eeviXKliZq",
         "status": "HHh0jJ9Md9",
         "unit_id": 9252,
         "unit_type": 233,
         "unit_type_name_advertised": "z70wnPQpSk"
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
   "ip": "QsLiFD4MY7",
   "machine": "O3gDk8Bg7W",
   "platform_instances": [
      {
         "ip": "9LLxq2zGNO",
         "machine": "6q1Xh3S7gY",
         "reason": "ekwHUMGhWz",
         "status": "Gpld7aFPfY"
      }
   ],
   "proposal_id": "JK6SV75aze",
   "reason": "oT0L8r30xv",
   "role": "Tnj31WE1Wf",
   "status": "9y7vf8sRN3",
   "unit_id": 20008,
   "unit_type": 67,
   "unit_type_name_advertised": "cu3ujt5jSr"
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

