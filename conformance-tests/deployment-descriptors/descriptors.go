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

// checkRoundTrip checks the contract behaviorally: a descriptor the builder
// produces marshals to JSON the platform reads back with every field intact.
func checkRoundTrip() error {
	// These values are named so the descriptor and its peer topology stay easy to
	// compare without scattering literals through the fixture.
	const (
		site        = "north"
		machine     = "sensor"
		machineIP   = "10.0.1.10"
		peerMachine = "gateway"
		peerIP      = "10.0.1.11"
	)

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
		Instances:   builderdeployment.InstancePolicy{WarmStandby: true},
		EventFabric: builderdeployment.EventFabric{
			Peers: []builderdeployment.EventFabricPeer{
				{Site: site, Machine: peerMachine, IP: peerIP},
			},
		},
	}

	data, err := json.Marshal(built)
	if err != nil {
		return err
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	instances, ok := wire["instances"]
	if !ok {
		return fmt.Errorf("builder descriptor omitted instances")
	}
	var policy map[string]json.RawMessage
	if err := json.Unmarshal(instances, &policy); err != nil {
		return err
	}
	if _, ok := policy["warm_standby"]; !ok {
		return fmt.Errorf("builder descriptor omitted instances.warm_standby")
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
		Instances:   platformdeployment.InstancePolicy{WarmStandby: true},
		EventFabric: platformdeployment.EventFabric{
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
