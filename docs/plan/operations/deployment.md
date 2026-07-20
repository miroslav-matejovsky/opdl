# Deploying the platform

## 1. Validate the topology before building

Every machine must explicitly define its NATS ports and standby decision:

```hcl
platform {
  nats {
    client_port  = 4222
    cluster_port = 6222
  }

  standby {
    disabled = false
  }
}
```

Set `standby.disabled = true` when no second local process is required. The
standby block is mandatory in both cases. Ports are numbers, not addresses. The
builder combines them with the machine IP and resolves routes and server lists.
Runtime TOML cannot override that topology.

Storage selection is deterministic per site:

| Site machines | Storage nodes | Journal replicas | Cluster listeners |
| ---: | ---: | ---: | ---: |
| 1 | First machine by sorted name | 1 | 0 |
| 2 | First machine by sorted name | 1 | 0 |
| 3 or more | First 3 machines by sorted name | 3 | 3 |

One- and two-machine sites do not tolerate journal-node loss. A site that must
tolerate one storage-node failure needs at least three machines with durable
storage on the first three names.

## 2. Inspect each package

Each machine package contains:

- the machine-specific executable;
- `deployment.json`, the resolved identity and Event Fabric topology;
- `manifest.json`, including the executable and primary or standby arguments;
- `release.json` and `checksums.txt` for provenance and integrity.

Verify `checksums.txt` before installation. Install only the package for the
machine named by `manifest.json`. A binary's identity is compiled in and cannot
be reassigned with runtime configuration.

## 3. Prepare local directories and secrets

Use separate local paths for:

- `instance_dir`: fence and process status files;
- `event_fabric.nats.data_dir`: durable JetStream journal data;
- `operations.event_dir`: optional operational JSONL retention.

The primary and standby must share `instance_dir` and the same configuration.
`instance_dir` must be on a local filesystem because active ownership uses an OS
file lock. Do not place it on NFS, SMB, or another network filesystem.

The journal path must be persistent, capacity-monitored storage. The runtime
creates a deployment-identity subdirectory and never deletes it. Never copy one
node's store into two live nodes or start two machines from the same store.

For non-loopback NATS addresses, configure a separate credentials file:

```toml
username = "opdl-site"
password = "replace-with-secret-management-value"
```

Restrict that file to the service identity. Its content is never logged. Current
username/password authentication does not encrypt traffic. Use a trusted network
boundary until TLS or mutual TLS is part of the deployment contract.

## 4. Write the runtime configuration

Use the checked-in `platform/config.toml` as the schema reference:

```toml
address = "10.0.1.10:8080"
read_header_timeout = "5s"
shutdown_timeout = "10s"
instance_dir = "/var/lib/opdl/instance"
lag_bound = "30s"

[operations]
event_dir = "/var/log/opdl/events"

[event_fabric.nats]
data_dir = "/var/lib/opdl/nats"
startup_timeout = "30s"
catch_up_timeout = "30s"
credentials_file = "/etc/opdl/nats-credentials.toml"
```

`operations.event_dir` may be omitted or empty. Structured events still go to
stderr. Unknown TOML keys fail startup. In particular, do not add client,
cluster, monitor, route, or server overrides.

## 5. Configure firewall rules

Allow only the minimum paths:

- public API port: only approved API clients to each active machine;
- NATS client port: OPDL machines in the same site to the selected storage
  machines;
- NATS cluster port: selected storage machines in the same site to each other;
- no NATS monitor port.

Bind addresses come from each machine's exact blueprint IP. Do not use wildcard
addresses. A client-only machine binds neither NATS port. A one-storage-node site
does not bind its authored cluster port because it has no route peer.

## 6. Configure services

Read launch arguments from `manifest.json`:

- primary: executable plus `-config <path> -instance primary`;
- standby, when present: executable plus `-config <path> -instance standby`.

Capture stdout and stderr in the service manager. Preserve stderr as structured
JSON-capable text. Do not discard it when JSONL retention is enabled; stderr is
the fallback when the JSONL sink fails.

Startup order:

1. Start all selected storage machines together for a three-replica cold site.
2. Start other site machines after the journal is reachable.
3. Start each preferred primary and wait for its status to report `active`.
4. Start its standby, when configured, and wait for status `standby` with
   `promotable=true` and an empty `last_error`.

For full shutdown, stop primary services and then standby services. A standby may
briefly acquire the fence between those stops, so the second stop is mandatory.

## 7. Deployment acceptance checks

- The process emits `platform.process_started`.
- Storage nodes emit `event_fabric.server_ready`; client-only nodes do not.
- Every node emits `event_fabric.client_connected` and
  `event_fabric.journal_ready`.
- Active nodes emit `platform.site_ready` and `platform.api_listening`.
- Standbys emit `platform.standby_ready` and `platform.standby_waiting`.
- Status `updated_at` advances every second.
- Active status has `state=active`, `promotable=true`, `last_error` absent, and
  `applied >= high_water` after catch-up.
- No process listens on a NATS monitor port.
