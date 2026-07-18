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
	// Instances is the machine's warm-standby policy. It is always present.
	Instances InstancePolicy `json:"instances"`
	// EventFabric is the resolved Event Fabric topology for this machine.
	EventFabric EventFabric `json:"event_fabric"`
}

// Features are the capability switches carried from the project onto a machine.
type Features struct {
	Chaos bool `json:"chaos"`
}

// InstancePolicy is a machine's resolved warm-standby policy: whether the machine
// runs a second local process (a warm standby slot) alongside its active process.
// It is always present in a generated descriptor, so a reader never has to infer
// the default.
type InstancePolicy struct {
	// WarmStandby enables one warm standby slot for the machine. An omitted
	// blueprint policy resolves to true; an explicit false runs a single slot.
	WarmStandby bool `json:"warm_standby"`
}

// EventFabric is this machine's resolved view of the site's Event Fabric: the
// peers it forms that fabric with. The descriptor already carries this machine's
// identity, so EventFabric contains only the additional topology. The builder
// derives it from the project topology and stages it here, so the platform boots
// knowing its membership and discovers nothing at runtime.
//
// It is transport-neutral: identities and addresses only, with no ports, adapter
// names, or protocol settings. The Event Fabric adapter derives what it needs
// from these addresses, which is what lets the transport be replaced without
// changing the deployment contract.
type EventFabric struct {
	// Peers are the other Event Fabric members of this machine's site, ordered by
	// machine name. A machine never lists itself, and the fabric spans exactly
	// one site. A single-machine site has no peers and forms a one-member fabric.
	Peers []EventFabricPeer `json:"peers"`
}

// EventFabricPeer is one other Event Fabric member this machine expects to meet.
type EventFabricPeer struct {
	// Site is the peer's site, always equal to this machine's site.
	Site string `json:"site"`
	// Machine is the peer's machine identity.
	Machine string `json:"machine"`
	// IP is the address the peer's Event Fabric member is reached on.
	IP string `json:"ip"`
}
