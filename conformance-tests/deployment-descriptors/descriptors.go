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
const (
	site            = "north"
	machine         = "sensor"
	machineIP       = "10.0.1.10"
	peerMachine     = "gateway"
	peerIP          = "10.0.1.11"
	clientAddr      = "10.0.1.10:4222"
	clusterAddr     = "10.0.1.10:6222"
	peerClientAddr  = "10.0.1.11:4222"
	peerClusterAddr = "10.0.1.11:6222"
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
	built := builderdeployment.Descriptor{
		Platform:    "opdl",
		Project:     "customer-a",
		Environment: "production",
		Site:        site,
		Machine:     machine,
		Role:        "sensor-node",
		IP:          machineIP,
		Services:    []string{"sensor-services", "core-services"},
		Features:    builderdeployment.Features{Chaos: true},
		Slots: builderdeployment.Slots{
			Primary: builderdeployment.Slot{Disabled: false},
			Standby: builderdeployment.Slot{Disabled: standbyDisabled},
		},
		EventFabric: builderdeployment.EventFabric{
			Nats: builderdeployment.EventFabricNats{
				ClientAddress:  clientAddr,
				ClusterAddress: clusterAddr,
				Routes:         []string{peerClusterAddr},
				Servers:        []string{clientAddr, peerClientAddr},
			},
			Peers: []builderdeployment.EventFabricPeer{
				{Site: site, Machine: peerMachine, IP: peerIP},
			},
		},
	}

	data, err := json.Marshal(built)
	if err != nil {
		return err
	}
	if err := checkWireShape(data); err != nil {
		return err
	}

	var got platformdeployment.Descriptor
	if err := json.Unmarshal(data, &got); err != nil {
		return err
	}

	want := platformdeployment.Descriptor{
		Platform:    "opdl",
		Project:     "customer-a",
		Environment: "production",
		Site:        site,
		Machine:     machine,
		Role:        "sensor-node",
		IP:          machineIP,
		Services:    []string{"sensor-services", "core-services"},
		Features:    platformdeployment.Features{Chaos: true},
		Slots: platformdeployment.Slots{
			Primary: platformdeployment.Slot{Disabled: false},
			Standby: platformdeployment.Slot{Disabled: standbyDisabled},
		},
		EventFabric: platformdeployment.EventFabric{
			Nats: platformdeployment.EventFabricNats{
				ClientAddress:  clientAddr,
				ClusterAddress: clusterAddr,
				Routes:         []string{peerClusterAddr},
				Servers:        []string{clientAddr, peerClientAddr},
			},
			Peers: []platformdeployment.EventFabricPeer{
				{Site: site, Machine: peerMachine, IP: peerIP},
			},
		},
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
func checkWireShape(data []byte) error {
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	slots, ok := wire["slots"]
	if !ok {
		return fmt.Errorf("builder descriptor omitted slots")
	}
	var policy map[string]json.RawMessage
	if err := json.Unmarshal(slots, &policy); err != nil {
		return err
	}
	for _, slot := range []string{"primary", "standby"} {
		raw, ok := policy[slot]
		if !ok {
			return fmt.Errorf("builder descriptor omitted slots.%s", slot)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		if _, ok := fields["disabled"]; !ok {
			return fmt.Errorf("builder descriptor omitted slots.%s.disabled", slot)
		}
		if _, ok := fields["event_fabric"]; ok {
			return fmt.Errorf("builder descriptor put event_fabric on slots.%s: NATS endpoints are machine-level and shared by both slots", slot)
		}
	}

	fabric, ok := wire["event_fabric"]
	if !ok {
		return fmt.Errorf("builder descriptor omitted event_fabric")
	}
	var fabricFields map[string]json.RawMessage
	if err := json.Unmarshal(fabric, &fabricFields); err != nil {
		return err
	}
	nats, ok := fabricFields["nats"]
	if !ok {
		return fmt.Errorf("builder descriptor omitted event_fabric.nats")
	}
	var natsFields map[string]json.RawMessage
	if err := json.Unmarshal(nats, &natsFields); err != nil {
		return err
	}
	if _, ok := natsFields["monitor_address"]; ok {
		return fmt.Errorf("builder descriptor carries event_fabric.nats.monitor_address: the platform runs no NATS monitoring listener")
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
