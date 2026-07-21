package deploymentdescriptors

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	builderdeployment "github.com/miroslav-matejovsky/opdl/builder/deployment"
	platformdeployment "github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/utils/jsonfields"
)

// Run performs the deployment descriptor conformance check: it confirms the
// builder's and the platform's descriptors describe the same contract, both
// structurally and by round-tripping a built descriptor through the platform's
// type.
func Run() error {
	if err := checkContractsMatch(); err != nil {
		return err
	}
	return checkRoundTrip()
}

// checkContractsMatch fails if the builder's deployment descriptor and the
// platform's diverge in JSON shape. The two are separate Go types in separate
// modules on purpose (the platform must not depend on the build tool), so this
// check is what keeps them the same contract: add, rename, or retype a field on
// one side without matching the other and it fails.
func checkContractsMatch() error {
	builderSig := signature(reflect.TypeFor[builderdeployment.Descriptor]())
	platformSig := signature(reflect.TypeFor[platformdeployment.Descriptor]())
	if builderSig != platformSig {
		return fmt.Errorf("builder and platform deployment descriptors have diverged:\n  builder:  %s\n  platform: %s", builderSig, platformSig)
	}
	return nil
}

// These values are named so the descriptor and its peer topology stay easy to
// compare without scattering literals through the fixture.
//
// The fixture is a two-machine site where the peer stores the journal and this
// machine does not, and where both machines deploy both instances. That shape
// exercises what the contract now has to carry: two peers on one machine sharing
// an ip and differing by port, and a per-instance NATS topology rather than a
// machine-level one.
const (
	site      = "north"
	machine   = "sensor"
	machineIP = "10.0.1.10"

	apiAddr        = "10.0.1.10:8080"
	clientAddr     = "10.0.1.10:4222"
	clusterAddr    = "10.0.1.10:6222"
	standbyAPIAddr = "10.0.1.10:8081"
	standbyClient  = "10.0.1.10:4322"
	standbyCluster = "10.0.1.10:6322"

	peerMachine        = "gateway"
	peerIP             = "10.0.1.11"
	peerAPIAddr        = "10.0.1.11:8080"
	peerClientAddr     = "10.0.1.11:4222"
	peerClusterAddr    = "10.0.1.11:6222"
	peerStandbyAPI     = "10.0.1.11:8081"
	peerStandbyClient  = "10.0.1.11:4322"
	peerStandbyCluster = "10.0.1.11:6322"
)

// checkRoundTrip checks the contract behaviorally: a descriptor the builder
// produces marshals to JSON the platform reads back with every field intact.
//
// Both standby policies are checked. The standby decision is a bool, so an
// enabled and a disabled machine differ by one JSON value that has a usable
// zero: a round trip that only ever carried one of them would pass while the
// other silently decoded to the default.
func checkRoundTrip() error {
	for _, standbyDisabled := range []bool{false, true} {
		if err := checkRoundTripFor(standbyDisabled); err != nil {
			return fmt.Errorf("standby disabled=%t: %w", standbyDisabled, err)
		}
	}
	return nil
}

func checkRoundTripFor(standbyDisabled bool) error {
	// This machine does not store the journal, so neither of its instances routes
	// and both reach the journal through the peer machine's two servers.
	servers := []string{peerClientAddr, peerStandbyClient}

	builtStandby := builderdeployment.Instance{Disabled: true}
	wantStandby := platformdeployment.Instance{Disabled: true}
	var builtLock *builderdeployment.Lock
	var wantLock *platformdeployment.Lock
	if !standbyDisabled {
		builtStandby = builderdeployment.Instance{
			Disabled:   false,
			APIAddress: standbyAPIAddr,
			Nats: &builderdeployment.Nats{
				ClientAddress:  standbyClient,
				ClusterAddress: standbyCluster,
				Routes:         []string{},
				Servers:        servers,
			},
		}
		wantStandby = platformdeployment.Instance{
			Disabled:   false,
			APIAddress: standbyAPIAddr,
			Nats: &platformdeployment.Nats{
				ClientAddress:  standbyClient,
				ClusterAddress: standbyCluster,
				Routes:         []string{},
				Servers:        servers,
			},
		}
		builtLock = &builderdeployment.Lock{WindowsMutex: "Global\\opdl-customer-a-north-sensor"}
		wantLock = &platformdeployment.Lock{WindowsMutex: "Global\\opdl-customer-a-north-sensor"}
	}

	// Peers are ordered by machine name, then Primary before Standby, and include
	// this machine's own instances.
	builtPeers := []builderdeployment.Peer{
		{Site: site, Machine: peerMachine, Role: builderdeployment.RolePrimary, IP: peerIP, APIAddress: peerAPIAddr,
			Nats: builderdeployment.PeerNats{ClientAddress: peerClientAddr, ClusterAddress: peerClusterAddr}},
		{Site: site, Machine: peerMachine, Role: builderdeployment.RoleStandby, IP: peerIP, APIAddress: peerStandbyAPI,
			Nats: builderdeployment.PeerNats{ClientAddress: peerStandbyClient, ClusterAddress: peerStandbyCluster}},
		{Site: site, Machine: machine, Role: builderdeployment.RolePrimary, IP: machineIP, APIAddress: apiAddr,
			Nats: builderdeployment.PeerNats{ClientAddress: clientAddr, ClusterAddress: clusterAddr}},
	}
	wantPeers := []platformdeployment.Peer{
		{Site: site, Machine: peerMachine, Role: platformdeployment.RolePrimary, IP: peerIP, APIAddress: peerAPIAddr,
			Nats: platformdeployment.PeerNats{ClientAddress: peerClientAddr, ClusterAddress: peerClusterAddr}},
		{Site: site, Machine: peerMachine, Role: platformdeployment.RoleStandby, IP: peerIP, APIAddress: peerStandbyAPI,
			Nats: platformdeployment.PeerNats{ClientAddress: peerStandbyClient, ClusterAddress: peerStandbyCluster}},
		{Site: site, Machine: machine, Role: platformdeployment.RolePrimary, IP: machineIP, APIAddress: apiAddr,
			Nats: platformdeployment.PeerNats{ClientAddress: clientAddr, ClusterAddress: clusterAddr}},
	}
	if !standbyDisabled {
		builtPeers = append(builtPeers, builderdeployment.Peer{
			Site: site, Machine: machine, Role: builderdeployment.RoleStandby, IP: machineIP, APIAddress: standbyAPIAddr,
			Nats: builderdeployment.PeerNats{ClientAddress: standbyClient, ClusterAddress: standbyCluster},
		})
		wantPeers = append(wantPeers, platformdeployment.Peer{
			Site: site, Machine: machine, Role: platformdeployment.RoleStandby, IP: machineIP, APIAddress: standbyAPIAddr,
			Nats: platformdeployment.PeerNats{ClientAddress: standbyClient, ClusterAddress: standbyCluster},
		})
	}

	built := builderdeployment.Descriptor{
		Platform:       "opdl",
		Project:        "customer-a",
		Environment:    "production",
		Site:           site,
		Machine:        machine,
		MachineProfile: "sensor-node",
		IP:             machineIP,
		Services:       []string{"sensor-services", "core-services"},
		Features:       builderdeployment.Features{Chaos: true},
		Instances: builderdeployment.Instances{
			Primary: builderdeployment.Instance{
				Disabled:   false,
				APIAddress: apiAddr,
				Nats: &builderdeployment.Nats{
					ClientAddress:  clientAddr,
					ClusterAddress: clusterAddr,
					Routes:         []string{},
					Servers:        servers,
				},
			},
			Standby: builtStandby,
		},
		Lock:  builtLock,
		Peers: builtPeers,
	}

	data, err := json.Marshal(built)
	if err != nil {
		return err
	}
	if err := checkWireShape(data, standbyDisabled); err != nil {
		return err
	}

	var got platformdeployment.Descriptor
	if err := json.Unmarshal(data, &got); err != nil {
		return err
	}

	want := platformdeployment.Descriptor{
		Platform:       "opdl",
		Project:        "customer-a",
		Environment:    "production",
		Site:           site,
		Machine:        machine,
		MachineProfile: "sensor-node",
		IP:             machineIP,
		Services:       []string{"sensor-services", "core-services"},
		Features:       platformdeployment.Features{Chaos: true},
		Instances: platformdeployment.Instances{
			Primary: platformdeployment.Instance{
				Disabled:   false,
				APIAddress: apiAddr,
				Nats: &platformdeployment.Nats{
					ClientAddress:  clientAddr,
					ClusterAddress: clusterAddr,
					Routes:         []string{},
					Servers:        servers,
				},
			},
			Standby: wantStandby,
		},
		Lock:  wantLock,
		Peers: wantPeers,
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("builder descriptor did not round-trip into the platform descriptor:\n  got:  %+v\n  want: %+v", got, want)
	}
	return nil
}

// checkWireShape checks the JSON the builder emits carries every decision the
// platform is required to read explicitly, and carries no monitor endpoint.
//
// The presence checks are on the wire rather than on the decoded value because
// that is where the distinction exists: once decoded, an omitted "disabled" and
// an explicit false are the same Go value, and the platform's requirement that
// the field be stated can only be proven against the bytes.
func checkWireShape(data []byte, standbyDisabled bool) error {
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	instances, ok := wire["instances"]
	if !ok {
		return fmt.Errorf("builder descriptor omitted instances")
	}
	var byRole map[string]json.RawMessage
	if err := json.Unmarshal(instances, &byRole); err != nil {
		return err
	}
	for _, role := range []string{"primary", "standby"} {
		raw, ok := byRole[role]
		if !ok {
			return fmt.Errorf("builder descriptor omitted instances.%s", role)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		if _, ok := fields["disabled"]; !ok {
			return fmt.Errorf("builder descriptor omitted instances.%s.disabled", role)
		}
		nats, ok := fields["nats"]
		if !ok {
			continue
		}
		var natsFields map[string]json.RawMessage
		if err := json.Unmarshal(nats, &natsFields); err != nil {
			return err
		}
		if _, ok := natsFields["monitor_address"]; ok {
			return fmt.Errorf("builder descriptor carries instances.%s.nats.monitor_address: the platform runs no NATS monitoring listener", role)
		}
	}

	// The endpoints an instance binds belong to that instance. A machine-level
	// NATS or API block would be an endpoint with two owners, which is what the
	// per-instance shape exists to make impossible.
	for _, field := range []string{"event_fabric", "nats", "api_address"} {
		if _, ok := wire[field]; ok {
			return fmt.Errorf("builder descriptor carries machine-level %q: endpoints belong to an instance", field)
		}
	}
	if err := verifyWireLock(wire, standbyDisabled); err != nil {
		return err
	}
	if _, ok := wire["peers"]; !ok {
		return fmt.Errorf("builder descriptor omitted peers")
	}
	return nil
}

func verifyWireLock(wire map[string]json.RawMessage, standbyDisabled bool) error {
	if standbyDisabled {
		if _, ok := wire["lock"]; ok {
			return fmt.Errorf("builder descriptor carries lock when standby is disabled")
		}
		return nil
	}
	lockRaw, ok := wire["lock"]
	if !ok {
		return fmt.Errorf("builder descriptor omitted lock when standby is deployed")
	}
	var lockFields map[string]json.RawMessage
	if err := json.Unmarshal(lockRaw, &lockFields); err != nil {
		return err
	}
	if _, ok := lockFields["windows_mutex"]; !ok {
		return fmt.Errorf("builder descriptor omitted lock.windows_mutex")
	}
	return nil
}

// signature renders a type's JSON wire shape structurally: each field's JSON name
// mapped to its kind, recursing into structs and slices. It ignores Go type
// identity and package, so two descriptors from different modules that encode to
// the same JSON produce the same signature.
func signature(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Struct:
		fields, err := jsonfields.Fields(t)
		if err != nil {
			return t.Kind().String()
		}
		parts := make([]string, 0, len(fields))
		for _, f := range fields {
			parts = append(parts, f.Name+":"+signature(f.Type))
		}
		sort.Strings(parts)
		return "{" + strings.Join(parts, ",") + "}"
	case reflect.Slice, reflect.Array:
		return "[]" + signature(t.Elem())
	case reflect.Pointer:
		return "*" + signature(t.Elem())
	default:
		return t.Kind().String()
	}
}
