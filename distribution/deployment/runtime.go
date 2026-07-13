package deployment

import "fmt"

// Operational parameter keys the platform understands. They are shared
// vocabulary: the builder declares them in a descriptor's Runtime policy and the
// platform reads them from the resolved effective configuration.
const (
	// ParamHTTPAddr is the loopback address the operational surface binds.
	ParamHTTPAddr = "http_addr"
	// ParamLogLevel is the log verbosity, e.g. "info" or "debug".
	ParamLogLevel = "log_level"
	// ParamClusterBindAddr is the host:port address used for cluster gossip.
	// An empty value disables cluster membership for the process.
	ParamClusterBindAddr = "cluster_bind_addr"
	// ParamClusterAdvertiseAddr is the optional host:port address announced to
	// other cluster members when it differs from the bind address.
	ParamClusterAdvertiseAddr = "cluster_advertise_addr"
	// ParamClusterJoin is a comma-separated list of host:port seed members.
	ParamClusterJoin = "cluster_join"
	// ParamDataBindAddr is the host:port address for Olric's Redis-compatible
	// client surface. An empty value disables distributed data for the process.
	ParamDataBindAddr = "data_bind_addr"
	// ParamDataMemberlistAddr is the host:port address Olric uses for
	// peer-to-peer membership and partition ownership.
	ParamDataMemberlistAddr = "data_memberlist_addr"
	// ParamDataJoin is a comma-separated list of Olric memberlist seed members.
	ParamDataJoin = "data_join"
	// ParamEventsDir is the directory platform events are written to as JSONL. An
	// empty value keeps events in memory only (visible on the operational
	// surface, not persisted).
	ParamEventsDir = "events_dir"

	// --- Bootstrap parameters ---
	//
	// These are the locally provisioned values a node needs before the site
	// authority and central configuration are reachable. They are
	// deployment-owned with a runtime override, and ship empty (slot ships
	// "single") so a node runs standalone -- no internal listener, no authority
	// connection -- until an operator provisions them on the machine. They are
	// bootstrap only: they carry no centrally managed domain values.

	// ParamPlatformBindAddr is the host:port the internal node listener binds. An
	// empty value keeps the node standalone: no internal node listener is opened
	// and distributed mode stays disabled.
	ParamPlatformBindAddr = "platform_bind_addr"
	// ParamPlatformAdvertiseAddr is the host:port peers dial to reach this node.
	// It is required when the bind address is a wildcard and is never inferred
	// from request headers or interface enumeration.
	ParamPlatformAdvertiseAddr = "platform_advertise_addr"
	// ParamPlatformInstanceSlot is the process slot: "single", "primary", or
	// "secondary". An empty value is treated as "single".
	ParamPlatformInstanceSlot = "platform_instance_slot"
	// ParamPlatformAuthorities is an ordered, comma-separated list of authority
	// host:port endpoints used for bootstrap. The first reachable compatible
	// authority wins. Empty means no remote authority (standalone).
	ParamPlatformAuthorities = "platform_authorities"
	// ParamPlatformPeerEndpoints is a temporary ordered, comma-separated list of
	// direct node endpoints used by the Step 3 fabric before registry discovery
	// exists. Active streams retain an established endpoint during a registry
	// outage; Step 4 replaces this bootstrap list with discovered peers.
	ParamPlatformPeerEndpoints = "platform_peer_endpoints"
	// ParamPlatformSecurityMode is the internal node protocol security mode:
	// empty (standalone/none), "development-token", or "tls". Distributed
	// production mode must not run plaintext; see the platform node config.
	ParamPlatformSecurityMode = "platform_security_mode"
	// ParamPlatformActivationName is the shared local activation name a
	// primary/secondary pair contends on for single-active ownership. Empty for a
	// single-process deployment.
	ParamPlatformActivationName = "platform_activation_name"
)

// Owner names who owns a runtime parameter's value.
type Owner string

const (
	// OwnerDeployment means the deployment sets the value. Whether local runtime
	// configuration may replace it depends on Parameter.Override.
	OwnerDeployment Owner = "deployment"
	// OwnerRuntime means the target machine provides the value. The deployment
	// may still offer a Default as a fallback.
	OwnerRuntime Owner = "runtime"
)

// Parameter is one operational setting the platform reads at startup, together
// with the policy that decides whether local runtime configuration may set or
// override it. The distinction between a deployment-owned and a runtime-owned
// value is not the parameter itself but its ownership and override policy. Three
// shapes are expressible:
//
//   - deployment-only:  Owner deployment, Override false. Fixed by the
//     deployment; a runtime value for it is rejected.
//   - deployment default with runtime override: Owner deployment, Override true.
//     Default applies unless local runtime configuration replaces it.
//   - runtime-only: Owner runtime. The machine provides the value; Default, if
//     set, is the fallback when the machine provides none.
type Parameter struct {
	// Key is the parameter name, unique within a Runtime policy.
	Key string `json:"key"`
	// Default is the deployment-provided value, if any.
	Default string `json:"default,omitempty"`
	// Owner is who owns the value: deployment or runtime.
	Owner Owner `json:"owner"`
	// Override, when true and Owner is deployment, permits local runtime
	// configuration to replace Default.
	Override bool `json:"override,omitempty"`
	// Required, when true, means the effective configuration must have a
	// non-empty value for this parameter.
	Required bool `json:"required,omitempty"`
}

// Runtime is the deployment's declaration of operational parameters and their
// ownership/override policy. It is the deployment-owned half of the effective
// configuration; the machine-owned half is RuntimeConfig.
type Runtime struct {
	Parameters []Parameter `json:"parameters,omitempty"`
}

// validate checks that a runtime policy is well formed: keys are non-empty and
// unique and each owner is a known value.
func (r Runtime) validate() error {
	seen := make(map[string]bool, len(r.Parameters))
	for _, p := range r.Parameters {
		if p.Key == "" {
			return fmt.Errorf("descriptor: runtime parameter with empty key")
		}
		if seen[p.Key] {
			return fmt.Errorf("descriptor: runtime parameter %q declared more than once", p.Key)
		}
		seen[p.Key] = true
		if p.Owner != OwnerDeployment && p.Owner != OwnerRuntime {
			return fmt.Errorf("descriptor: runtime parameter %q has invalid owner %q", p.Key, p.Owner)
		}
	}
	return nil
}

// DefaultRuntime is the canonical policy for the significant, deployment-owned
// operational parameters stamped into every machine's descriptor. It is the
// single source of truth for the descriptor's runtime section: the builder writes
// it into generated descriptors, the embedded mock descriptor mirrors it, and
// scenario descriptors reuse it.
//
// It deliberately declares only the deployment-significant parameters — the
// operational surface address, log level, cluster/data/events wiring, and the
// platform node bootstrap (bind/advertise/slot/authorities/peer endpoints/security) — not the
// platform's own operational tuning (timeouts, buffer sizes). Those are
// platform-owned and live in the platform's own runtime configuration, kept out
// of the deployment so a deployment describes what a machine is, not how the
// platform internally paces itself.
//
// Every parameter is deployment-owned with a runtime override: the deployment
// ships a working default, and the target machine may replace it through local
// runtime configuration. The address parameters (cluster_*, data_*, events_dir,
// platform_*) ship empty, which keeps clustering, distributed data, event
// persistence, internal node listener, and direct peer fabric off until an operator opts in; the
// process slot ships "single".
//
// There is deliberately no chaos_* parameter here. Chaos tuning is API-only: a
// fault is turned on through the authorized HTTP surface, never a config file or
// environment. The absence is the design, not an oversight; do not add one.
func DefaultRuntime() Runtime {
	dep := func(key, def string) Parameter {
		return Parameter{Key: key, Default: def, Owner: OwnerDeployment, Override: true}
	}
	return Runtime{Parameters: []Parameter{
		dep(ParamHTTPAddr, "127.0.0.1:8080"),
		dep(ParamLogLevel, "info"),
		dep(ParamClusterBindAddr, ""),
		dep(ParamClusterAdvertiseAddr, ""),
		dep(ParamClusterJoin, ""),
		dep(ParamDataBindAddr, ""),
		dep(ParamDataMemberlistAddr, ""),
		dep(ParamDataJoin, ""),
		dep(ParamEventsDir, ""),
		dep(ParamPlatformBindAddr, ""),
		dep(ParamPlatformAdvertiseAddr, ""),
		dep(ParamPlatformInstanceSlot, "single"),
		dep(ParamPlatformAuthorities, ""),
		dep(ParamPlatformPeerEndpoints, ""),
		dep(ParamPlatformSecurityMode, ""),
		dep(ParamPlatformActivationName, ""),
	}}
}

// RuntimeConfig is the local configuration provided on the target machine. It
// supplies values for runtime-owned parameters and overrides of
// deployment-defined parameters where the deployment policy permits. It is read
// from the machine, not baked into the deployment.
type RuntimeConfig struct {
	// Values are parameter keys to machine-provided values.
	Values map[string]string `json:"values,omitempty"`
}

// EffectiveConfig is the resolved operational configuration the platform runs
// from: the deployment descriptor plus the parameter values settled by applying
// local runtime configuration to the deployment's policy.
type EffectiveConfig struct {
	// Descriptor is the deployment the effective configuration was resolved from.
	Descriptor Descriptor
	params     map[string]string
}

// Param returns the effective value of a parameter and whether it is set.
func (e EffectiveConfig) Param(key string) (string, bool) {
	v, ok := e.params[key]
	return v, ok
}

// ParamOr returns the effective value of a parameter, or fallback if it has no
// value.
func (e EffectiveConfig) ParamOr(key, fallback string) string {
	if v, ok := e.params[key]; ok && v != "" {
		return v
	}
	return fallback
}

// Resolve settles the effective configuration from a deployment descriptor and
// the machine's local runtime configuration, enforcing the descriptor's
// parameter policy. It fails fast: a runtime value for an unknown parameter, or
// for a deployment-owned parameter that is not overridable, is an error rather
// than a silent no-op, and a required parameter left without a value is an
// error. Deployment defaults apply where the machine provides nothing.
func Resolve(d Descriptor, rc RuntimeConfig) (EffectiveConfig, error) {
	declared := make(map[string]Parameter, len(d.Runtime.Parameters))
	params := make(map[string]string, len(d.Runtime.Parameters))
	for _, p := range d.Runtime.Parameters {
		declared[p.Key] = p
		params[p.Key] = p.Default
	}

	for key, value := range rc.Values {
		p, ok := declared[key]
		if !ok {
			return EffectiveConfig{}, fmt.Errorf("runtime config: unknown parameter %q", key)
		}
		if p.Owner == OwnerDeployment && !p.Override {
			return EffectiveConfig{}, fmt.Errorf("runtime config: parameter %q is deployment-owned and not overridable", key)
		}
		params[key] = value
	}

	for _, p := range d.Runtime.Parameters {
		if p.Required && params[p.Key] == "" {
			return EffectiveConfig{}, fmt.Errorf("runtime config: parameter %q is required but has no value", p.Key)
		}
	}

	return EffectiveConfig{Descriptor: d, params: params}, nil
}
