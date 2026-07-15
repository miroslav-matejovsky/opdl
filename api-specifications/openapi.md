# OPDL Platform API

HTTP API served by the OPDL platform runtime.

**Version:** 0.1.0

## GET /registrations

List registration requests

**Response `200`**

Type: array<`Registration`>

Items:

| Field | Type | Required |
| --- | --- | --- |
| `ip` | string | yes |
| `machine` | string | yes |
| `platform_instances` | array<`PlatformInstanceRegistrationStatus`> | yes |
| `reason` | string |  |
| `role` | string |  |
| `status` | string | yes |
| `unit_id` | integer | yes |
| `unit_type` | integer | yes |
| `unit_type_name_advertised` | string | yes |

**Response `500`** — `Error`

| Field | Type | Required |
| --- | --- | --- |
| `code` | string | yes |

## POST /registrations

Request unit registration

**Request body** (required) — `RegistrationRequest`

| Field | Type | Required |
| --- | --- | --- |
| `role` | string |  |
| `unit_id` | integer | yes |
| `unit_type` | integer | yes |
| `unit_type_name_advertised` | string | yes |

**Response `202`**

**Response `400`** — `Error`

| Field | Type | Required |
| --- | --- | --- |
| `code` | string | yes |

**Response `409`** — `Error`

| Field | Type | Required |
| --- | --- | --- |
| `code` | string | yes |

**Response `500`** — `Error`

| Field | Type | Required |
| --- | --- | --- |
| `code` | string | yes |

## GET /registrations/{unit_type}/{unit_id}/status

Get registration request status

**Response `200`** — `Registration`

| Field | Type | Required |
| --- | --- | --- |
| `ip` | string | yes |
| `machine` | string | yes |
| `platform_instances` | array<`PlatformInstanceRegistrationStatus`> | yes |
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

**Response `500`** — `Error`

| Field | Type | Required |
| --- | --- | --- |
| `code` | string | yes |

