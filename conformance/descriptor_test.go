package conformance_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	builderdeployment "github.com/miroslav-matejovsky/opdl/builder/deployment"
	platformdeployment "github.com/miroslav-matejovsky/opdl/platform/deployment"
)

// TestDescriptorContractsMatch fails if the builder's deployment descriptor and
// the platform's diverge in JSON shape. The two are separate Go types in separate
// modules on purpose (the platform must not depend on the build tool), so this
// test is what keeps them the same contract: add, rename, or retype a field on
// one side without matching the other and it fails.
func TestDescriptorContractsMatch(t *testing.T) {
	builderSig := signature(reflect.TypeFor[builderdeployment.Descriptor]())
	platformSig := signature(reflect.TypeFor[platformdeployment.Descriptor]())
	require.Equal(t, builderSig, platformSig, "builder and platform deployment descriptors have diverged")
}

// TestBuilderDescriptorRoundTripsIntoPlatform checks the contract behaviorally: a
// descriptor the builder produces marshals to JSON the platform reads back with
// every field intact.
func TestBuilderDescriptorRoundTripsIntoPlatform(t *testing.T) {
	built := builderdeployment.Descriptor{
		Platform:    "opdl",
		Project:     "customer-a",
		Environment: "production",
		Site:        "north",
		Machine:     "sensor",
		Role:        "sensor-node",
		IP:          "10.0.1.10",
		Services:    []string{"sensor-services", "core-services"},
		Features:    builderdeployment.Features{Chaos: true, Redundancy: true},
	}

	data, err := json.Marshal(built)
	require.NoError(t, err)

	var got platformdeployment.Descriptor
	require.NoError(t, json.Unmarshal(data, &got))

	require.Equal(t, platformdeployment.Descriptor{
		Platform:    "opdl",
		Project:     "customer-a",
		Environment: "production",
		Site:        "north",
		Machine:     "sensor",
		Role:        "sensor-node",
		IP:          "10.0.1.10",
		Services:    []string{"sensor-services", "core-services"},
		Features:    platformdeployment.Features{Chaos: true, Redundancy: true},
	}, got)
}

// signature renders a type's JSON wire shape structurally: each field's JSON name
// mapped to its kind, recursing into structs and slices. It ignores Go type
// identity and package, so two descriptors from different modules that encode to
// the same JSON produce the same signature.
func signature(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Struct:
		parts := make([]string, 0, t.NumField())
		for f := range t.Fields() {
			if !f.IsExported() {
				continue
			}
			name := jsonName(f)
			if name == "-" {
				continue
			}
			parts = append(parts, name+":"+signature(f.Type))
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

// jsonName returns a field's JSON name: the json tag's name if present, else the
// Go field name.
func jsonName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name
	}
	if name := strings.Split(tag, ",")[0]; name != "" {
		return name
	}
	return f.Name
}
