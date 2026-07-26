package deploymentdescriptors

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	builderdeployment "github.com/miroslav-matejovsky/opdl/builder/deployment"
	platformconfig "github.com/miroslav-matejovsky/opdl/platform/config"
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
	platformSig := signature(reflect.TypeFor[platformconfig.Descriptor]())
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

	// The api addresses are on loopback and the Event Fabric's are on the machine
	// ip. That split is the contract: the platform API is machine-local, so it is
	// resolved onto 127.0.0.1 and a peer carries no api address at all.
	dataDir                  = "D:/opdl/customer-a/north/sensor/primary"
	jetstreamStoreDir        = "D:/opdl/customer-a/north/sensor/primary/eventfabric/nats"
	apiAddr                  = "127.0.0.1:8080"
	clientAddr               = "10.0.1.10:4222"
	clusterAddr              = "10.0.1.10:6222"
	standbyDataDir           = "D:/opdl/customer-a/north/sensor/standby"
	standbyJetStreamStoreDir = "D:/opdl/customer-a/north/sensor/standby/eventfabric/nats"
	standbyAPIAddr           = "127.0.0.1:8081"
	standbyClient            = "10.0.1.10:4322"
	standbyCluster           = "10.0.1.10:6322"

	// The Primary Ownership lease a standby machine carries: a shared file and the
	// failover timings, all round-tripped through the platform's type intact.
	leaseFile                  = "D:/opdl/customer-a/north/sensor/lease"
	leaseDuration              = "15s"
	leaseRenewalInterval       = "5s"
	leaseHealthCheckInterval   = "2s"
	leaseFailbackStabilization = "30s"
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
	builtStandby := builderdeployment.Instance{Disabled: true}
	wantStandby := platformconfig.Instance{Disabled: true}
	var builtLease *builderdeployment.Lease
	var wantLease *platformconfig.Lease
	if !standbyDisabled {
		builtStandby = builderdeployment.Instance{
			Disabled:   false,
			DataDir:    standbyDataDir,
			APIAddress: standbyAPIAddr,
		}
		wantStandby = platformconfig.Instance{
			Disabled:   false,
			DataDir:    standbyDataDir,
			APIAddress: standbyAPIAddr,
		}
		builtLease = &builderdeployment.Lease{
			File:                  leaseFile,
			Duration:              leaseDuration,
			RenewalInterval:       leaseRenewalInterval,
			HealthCheckInterval:   leaseHealthCheckInterval,
			FailbackStabilization: leaseFailbackStabilization,
		}
		wantLease = &platformconfig.Lease{
			File:                  leaseFile,
			Duration:              leaseDuration,
			RenewalInterval:       leaseRenewalInterval,
			HealthCheckInterval:   leaseHealthCheckInterval,
			FailbackStabilization: leaseFailbackStabilization,
		}
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
		Instances: builderdeployment.Instances{
			Primary: builderdeployment.Instance{
				Disabled:   false,
				DataDir:    dataDir,
				APIAddress: apiAddr,
			},
			Standby: builtStandby,
		},
		Lease: builtLease,
	}

	data, err := json.Marshal(built)
	if err != nil {
		return err
	}
	if err := checkWireShape(data, standbyDisabled); err != nil {
		return err
	}

	var got platformconfig.Descriptor
	if err := json.Unmarshal(data, &got); err != nil {
		return err
	}

	want := platformconfig.Descriptor{
		Platform:       "opdl",
		Project:        "customer-a",
		Environment:    "production",
		Site:           site,
		Machine:        machine,
		MachineProfile: "sensor-node",
		IP:             machineIP,
		Services:       []string{"sensor-services", "core-services"},
		Instances: platformconfig.Instances{
			Primary: platformconfig.Instance{
				Disabled:   false,
				DataDir:    dataDir,
				APIAddress: apiAddr,
			},
			Standby: wantStandby,
		},
		Lease: wantLease,
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
	}

	// The endpoints an instance binds belong to that instance.
	for _, field := range []string{"event_fabric", "nats", "api_address"} {
		if _, ok := wire[field]; ok {
			return fmt.Errorf("builder descriptor carries machine-level %q: endpoints belong to an instance", field)
		}
	}
	return verifyWireLease(wire, standbyDisabled)
}

func verifyWireLease(wire map[string]json.RawMessage, standbyDisabled bool) error {
	if standbyDisabled {
		if _, ok := wire["lease"]; ok {
			return fmt.Errorf("builder descriptor carries lease when standby is disabled")
		}
		return nil
	}
	leaseRaw, ok := wire["lease"]
	if !ok {
		return fmt.Errorf("builder descriptor omitted lease when standby is deployed")
	}
	var leaseFields map[string]json.RawMessage
	if err := json.Unmarshal(leaseRaw, &leaseFields); err != nil {
		return err
	}
	for _, field := range []string{"file", "duration", "renewal_interval", "health_check_interval", "failback_stabilization"} {
		if _, ok := leaseFields[field]; !ok {
			return fmt.Errorf("builder descriptor omitted lease.%s", field)
		}
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
