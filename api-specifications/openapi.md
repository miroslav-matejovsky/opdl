# OPDL Platform API

HTTP API served by the OPDL platform runtime.

**Version:** 0.1.0

## GET /registrations

List registration proposals

**Response `200`**

Type: array<`Registration`>

Items:

| Field | Type | Required |
| --- | --- | --- |
| `ip` | string | yes |
| `machine` | string | yes |
| `platform_instances` | array<`PlatformInstanceRegistrationStatus`> | yes |
| `proposal_id` | string | yes |
| `reason` | string |  |
| `role` | string |  |
| `status` | string | yes |
| `unit_id` | integer | yes |
| `unit_type` | integer | yes |
| `unit_type_name_advertised` | string | yes |

## POST /registrations

Propose a unit registration

**Request body** (required) — `RegistrationRequest`

| Field | Type | Required |
| --- | --- | --- |
| `role` | string |  |
| `unit_id` | integer | yes |
| `unit_type` | integer | yes |
| `unit_type_name_advertised` | string | yes |

**Response `202`** — `ProposalAccepted`

| Field | Type | Required |
| --- | --- | --- |
| `proposal_id` | string | yes |
| `sequence` | integer | yes |

**Response `400`** — `Error`

| Field | Type | Required |
| --- | --- | --- |
| `code` | string | yes |

**Response `503`** — `Error`

| Field | Type | Required |
| --- | --- | --- |
| `code` | string | yes |

## GET /registrations/conflicts

List resolved registration conflicts

**Response `200`**

Type: array<`RegistrationConflict`>

Items:

| Field | Type | Required |
| --- | --- | --- |
| `losers` | array<`Registration`> | yes |
| `resolution_status` | string | yes |
| `unit_id` | integer | yes |
| `unit_type` | integer | yes |
| `winner` | `Registration` | yes |

**Response `500`** — `Error`

| Field | Type | Required |
| --- | --- | --- |
| `code` | string | yes |

## GET /registrations/{proposal_id}

Get a registration proposal's status

**Response `200`** — `Registration`

| Field | Type | Required |
| --- | --- | --- |
| `ip` | string | yes |
| `machine` | string | yes |
| `platform_instances` | array<`PlatformInstanceRegistrationStatus`> | yes |
| `proposal_id` | string | yes |
| `reason` | string |  |
| `role` | string |  |
| `status` | string | yes |
| `unit_id` | integer | yes |
| `unit_type` | integer | yes |
| `unit_type_name_advertised` | string | yes |

**Response `404`** — `Error`

| Field | Type | Required |
| --- | --- | --- |
| `code` | string | yes |

