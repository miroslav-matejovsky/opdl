# OPDL Platform API

HTTP API served by the OPDL platform runtime.

**Version:** 0.1.0

## GET /

Report platform status

**Response `200`** — `Status`

| Field | Type | Required |
| --- | --- | --- |
| `message` | string | yes |
| `status` | string | yes |

