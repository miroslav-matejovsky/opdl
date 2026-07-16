# Platform Configuration & Cleanup Plan

## Overview

This plan outlines moving platform runtime configuration constants out of code and into a structured TOML configuration file, separating the configuration loading schema into `file.go` for better readability, removing all in-code defaults in favor of explicit configuration requirements, and replacing `platform/doc.go` with `platform/README.md`.

These changes apply purely to the **runtime configuration file** of the platform (`config.toml`); they do **not** affect the static deployment descriptor (`deployment.Descriptor`) embedded during the builder phase.

---

## Goals & Invariants

1. **Strict No-Defaults Policy**: Code shall not supply fallback default values (`127.0.0.1:8080`, `1s`, `30s`, `10s`, `5s`) when configuration settings are omitted. Every required runtime setting must be explicitly specified and validated when loading the configuration file.
2. **TOML Configuration with Comments**: Replace JSON configuration (`config.json`) with TOML (`config.toml`). Provide an example configuration in `platform/config.toml` that uses comments to explain each field and document recommended values.
3. **Clean Code Separation (`file.go` vs `config.go`)**: Split `platform/internal/config` into `file.go` (TOML file schema, deserialization, and raw file validation) and `config.go` (runtime `Config` struct, composition with the embedded descriptor, duration parsing, accessor methods, and `Summary()`).
4. **Replace `platform/doc.go` with `README.md`**: Since `platform/` contains no top-level Go files other than `doc.go` (making it a directory containing subpackages rather than a Go package itself), replace `doc.go` with a clear `README.md`.

---

## Configuration Constants Identified for Migration

| Setting | Current Code Location | Current Constant / Hardcoded Value | Proposed TOML Key | Recommended Value / Description |
| --- | --- | --- | --- | --- |
| **Listen Address** | `platform/internal/config/config.go` | `defaultAddress = "127.0.0.1:8080"` | `address` | `"127.0.0.1:8080"` (Loopback bind address and port for the public HTTP API) |
| **Shutdown Timeout** | `platform/cmd/main.go` | `shutdownTimeout = 10 * time.Second` | `shutdown_timeout` | `"10s"` (Maximum grace period for in-flight requests during graceful server shutdown) |
| **Read Header Timeout** | `platform/cmd/main.go` | `5 * time.Second` (inline in `http.Server`) | `read_header_timeout` | `"5s"` (Maximum time allowed to read request headers to prevent slowloris attacks) |
| **Reconcile Interval** | `platform/internal/registration/reconciler.go` & `cmd/main.go` | `DefaultInterval = time.Second` | `[registration] reconcile_interval` | `"1s"` (Periodic scan interval for registration proposal reconciliation) |
| **Olric Start Timeout** | `platform/internal/fabric/olric/config.go` & `cmd/main.go` | `DefaultStartTimeout = 30 * time.Second` | `[fabric.olric] start_timeout` | `"30s"` (Maximum time allowed for the Olric memberlist fabric to become ready at startup) |
| **Olric Shutdown Grace** | `platform/internal/fabric/olric/olric.go` | `shutdownGrace = 10 * time.Second` | `[fabric.olric] shutdown_grace` | `"10s"` (Maximum grace period when tearing down an Olric member) |
| **Events Directory** | `platform/internal/config/config.go` | `""` (optional/disabled) | `events_dir` | `""` or path (Directory for JSONL domain event recording; empty disables recording) |

---

## Proposed TOML Schema & Example (`platform/config.toml`)

```toml
# OPDL Platform Runtime Configuration
#
# This file configures the runtime settings for an individual OPDL platform instance.
# All required values must be explicitly set; the runtime does not apply implicit code defaults.
# Identity properties (project, environment, site, machine name, topology IPs, and assigned services)
# come exclusively from the embedded deployment descriptor (`deployment.Descriptor`) compiled into the binary.

# The host:port address where the platform serves its public registration HTTP API.
# Recommended: "127.0.0.1:8080" for local testing, or the appropriate interface address in deployment.
address = "127.0.0.1:8080"

# Directory path where domain events (`platform.registration.*`, `platform.fabric.*`) are recorded in JSONL format.
# If set to an empty string (""), event recording is disabled.
events_dir = ""

# Maximum duration allowed for reading HTTP request headers before closing the connection.
# Recommended: "5s"
read_header_timeout = "5s"

# Maximum duration allowed for graceful server shutdown when terminating in-flight requests.
# Recommended: "10s"
shutdown_timeout = "10s"

[registration]
# Frequency at which the background reconciler scans and repairs site registration records.
# Recommended: "1s" (shorter intervals accept requests sooner; longer intervals reduce CPU overhead)
reconcile_interval = "1s"

[fabric.olric]
# Overrides for the embedded Olric distributed memory fabric adapter.
# In production, socket addresses (`client_address`, `memberlist_address`, `join`) are derived
# automatically from the deployment descriptor topology and can be omitted.

# Maximum duration allowed for the Olric memberlist join and initialization at startup.
# Recommended: "30s"
start_timeout = "30s"

# Maximum duration allowed for tearing down the Olric instance during shutdown.
# Recommended: "10s"
shutdown_grace = "10s"

# Optional socket overrides (uncomment when running multiple instances on a single development host):
# client_address = "127.0.0.1:3320"
# memberlist_address = "127.0.0.1:3322"
# join = ["127.0.0.1:3322"]
```

---

## Step-by-Step Implementation Plan

### Step 1: Add TOML Dependency & Split `platform/internal/config` (`file.go` + `config.go`)
- **Action**: Add TOML parsing capability (`github.com/BurntSushi/toml` or `github.com/pelletier/go-toml/v2`) to `platform/go.mod` (or verify workspace setup).
- **Create `platform/internal/config/file.go`**:
  - Define TOML schema structs: `file`, `Registration`, `Fabric`, and `FabricOlric` with `toml:"..."` tags.
  - Add top-level fields: `Address`, `EventsDir`, `ReadHeaderTimeout`, `ShutdownTimeout`.
  - Add `loadFile(path string) (file, error)`: read file from disk and decode using TOML.
  - Add `validateFile(f file) error`: enforce strict validation without fallback defaults. Require `Address`, `ReadHeaderTimeout`, `ShutdownTimeout`, `Registration.ReconcileInterval`, `Fabric.Olric.StartTimeout`, and `Fabric.Olric.ShutdownGrace` to be non-empty and valid durations/addresses. If `loadFile` is called on a non-existent path or missing required fields, return an explicit error explaining that configuration is required.
- **Refactor `platform/internal/config/config.go`**:
  - Keep `type Config struct` holding resolved values (`descriptor`, `address`, `eventsDir`, `readHeaderTimeout time.Duration`, `shutdownTimeout time.Duration`, `registration`, `fabric`).
  - Update `Load(configPath string) (*Config, error)` to call `loadFile`, parse all duration strings once, and store typed durations on `Config`.
  - Add accessor methods: `ReadHeaderTimeout() time.Duration`, `ShutdownTimeout() time.Duration`, and update `Summary()` to format all configured timeouts cleanly.

### Step 2: Update Platform Command & Domain Package Usage (`cmd/main.go`, `olric.go`)
- **Update `platform/cmd/main.go`**:
  - Change command flag default from `-config=config.json` to `-config=config.toml`.
  - Remove `shutdownTimeout` constant; use `cfg.ShutdownTimeout()` when setting up `context.WithTimeout` for server shutdown.
  - Remove hardcoded `ReadHeaderTimeout: 5 * time.Second`; use `cfg.ReadHeaderTimeout()` when constructing `http.Server`.
  - Remove `reconcileInterval(cfg.Registration())` fallback calculation that used `registration.DefaultInterval`. Instead, rely directly on the duration already parsed and validated by `config.Load`.
  - Pass configured timeouts (`StartTimeout`, `ShutdownGrace`) cleanly down to the Olric adapter configuration.
- **Update `platform/internal/fabric/olric/`**:
  - Remove or deprecate `DefaultStartTimeout` and `shutdownGrace` code constants where appropriate, ensuring `olric.Config` accepts explicit timeout and grace durations from `cfg.Fabric().Olric`.

### Step 3: Replace `config.json` with `config.toml` & Update Top-Level Documentation
- **Create `platform/config.toml`**:
  - Write the fully commented example configuration file in `platform/config.toml` with recommended values (`127.0.0.1:8080`, `5s`, `10s`, `1s`, `30s`, `10s`).
  - Delete `platform/config.json`.
- **Replace `platform/doc.go` with `platform/README.md`**:
  - Delete `platform/doc.go`.
  - Create `platform/README.md` summarizing the platform binary, command entry point (`cmd`), configuration loading (`internal/config`), and internal boundaries (`httpapi`, `registration`, `fabric`, `events`).

### Step 4: Update Tests & Scenarios across Workspace
- **Unit Tests (`config_test.go`, `cmd/main_test.go`, `olric_test.go`)**:
  - Update all tests that generate or read temporary config files to format TOML (`toml.Marshal` or `fmt.Sprintf` with TOML syntax) instead of JSON.
  - Update test assertions to ensure strict validation correctly rejects files with missing required fields (since no defaults are applied).
- **Scenario Tests (`scenarios/eventlog_test.go`, `scenarios/harness_test.go`)**:
  - Update `eventsConfig` helper to format valid TOML string containing all required fields (`address`, `events_dir`, `read_header_timeout`, `shutdown_timeout`, `[registration] reconcile_interval`, `[fabric.olric] start_timeout`, `[fabric.olric] shutdown_grace`).
  - Update `harness_test.go` to write `config-<name>.toml` and pass `-config=config-<name>.toml`.

### Step 5: Verification (`task all`)
- Run `task all` across the entire workspace to verify format, lint, unit tests, conformance tests, and end-to-end black-box scenarios pass cleanly with the new TOML configuration engine and no code defaults.
