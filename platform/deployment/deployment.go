package deployment

// Descriptor is one machine's deployment definition as the platform consumes it:
// its identity (platform, project, environment, site, machine, role, ip), the
// services it hosts, and the project features enabled on it.
//
// It mirrors the builder's deployment descriptor field for field. The platform
// keeps its own copy so the runtime does not depend on the build tool; the
// conformance-tests module checks the two representations stay compatible.
type Descriptor struct {
	// Platform identifies the product line the binary is built from.
	Platform string `json:"platform"`
	// Project, Environment, Site, Machine, Role place the machine in the topology.
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Site        string `json:"site"`
	Machine     string `json:"machine"`
	Role        string `json:"role"`
	// IP is the machine's network address.
	IP string `json:"ip"`
	// Services are the service groups this machine hosts.
	Services []string `json:"services"`
	// Features are the project capability switches enabled on the machine.
	Features Features `json:"features"`
	// PlatformInstances are the active platform processes configured on this
	// machine, ordered primary then optional secondary.
	PlatformInstances []PlatformInstance `json:"platform_instances"`
	// Fabric is the resolved platform fabric topology for this machine.
	Fabric Fabric `json:"fabric"`
}

// Features are the capability switches carried from the project onto a machine.
type Features struct {
	Chaos bool `json:"chaos"`
}

const (
	// PlatformInstancePrimary is the required platform process identity.
	PlatformInstancePrimary = "primary"
	// PlatformInstanceSecondary is the optional redundant process identity.
	PlatformInstanceSecondary = "secondary"
)

// PlatformInstance is one active platform process and its explicit endpoints.
type PlatformInstance struct {
	Name                    string `json:"name"`
	APIAddress              string `json:"api_address"`
	FabricClientAddress     string `json:"fabric_client_address"`
	FabricMemberlistAddress string `json:"fabric_memberlist_address"`
}

// Fabric is this machine's resolved view of the platform fabric: the peers it
// forms that fabric with. The descriptor already carries this machine's
// identity, so Fabric contains only the additional topology. The builder derives
// it from the project topology and stages it here, so the platform boots knowing
// its membership and discovers nothing at runtime.
//
// It carries explicit client and membership endpoints. Production code does not
// derive ports. Adapter-specific lifecycle tuning remains runtime configuration.
type Fabric struct {
	// Peers are the other fabric members of this machine's site, ordered by
	// machine name. A machine never lists itself, and the fabric spans exactly
	// one site. A single-machine site has no peers and forms a one-member fabric.
	Peers []FabricPeer `json:"peers"`
}

// FabricPeer is one other fabric member this machine expects to meet.
type FabricPeer struct {
	// Site is the peer's site, always equal to this machine's site.
	Site string `json:"site"`
	// Machine is the peer's machine identity.
	Machine string `json:"machine"`
	// Instance is the peer platform process identity: primary or secondary.
	Instance string `json:"instance"`
	// IP is the address the peer's fabric member is reached on.
	IP string `json:"ip"`
	// FabricClientAddress is the peer's explicit Olric client endpoint.
	FabricClientAddress string `json:"fabric_client_address"`
	// FabricMemberlistAddress is the peer's explicit membership endpoint.
	FabricMemberlistAddress string `json:"fabric_memberlist_address"`
}
