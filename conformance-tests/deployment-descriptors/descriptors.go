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

// The fixture is one machine deploying both instances. That shape exercises what
// the contract has to carry: two independent runtimes on one machine, each with
// its own local files, its own listener, and its own bounds on that listener.
const (
	site      = "north"
	machine   = "sensor"
	machineIP = "10.0.1.10"

	// The api addresses are on loopback. That is the contract: the platform API
	// is machine-local, so it is resolved onto 127.0.0.1 and never onto the
	// machine ip.
	// Each instance names every local file it owns outright, so a round trip that
	// dropped one, or resolved both instances onto the same path, would fail.
	eventsFile        = "D:/opdl/customer-a/north/sensor/primary/events.jsonl"
	stateFile         = "D:/opdl/customer-a/north/sensor/primary/state.json"
	apiAddr           = "127.0.0.1:8080"
	standbyEventsFile = "D:/opdl/customer-a/north/sensor/standby/events.jsonl"
	standbyStateFile  = "D:/opdl/customer-a/north/sensor/standby/state.json"
	standbyAPIAddr    = "127.0.0.1:8081"

	// The listener timeouts are on the instance record for the same reason the
	// api address is: the two instances bind their own listeners. They differ
	// between the roles here so a round trip that swapped them would fail.
	readHeaderTimeout        = "5s"
	shutdownTimeout          = "10s"
	standbyReadHeaderTimeout = "6s"
	standbyShutdownTimeout   = "11s"

	// The Primary Ownership lease a standby machine carries: a shared file, the
	// failover timings, and the projection lag bound that gates a failover, all
	// round-tripped through the platform's type intact.
	leaseFile                  = "D:/opdl/customer-a/north/sensor/lease"
	leaseDuration              = "15s"
	leaseRenewalInterval       = "5s"
	leaseHealthCheckInterval   = "2s"
	leaseFailbackStabilization = "30s"
	leaseLagBound              = "30s"
)

// checkRoundTrip checks the contract behaviorally: a descriptor the builder
// produces marshals to JSON the platform reads back with every field intact.
//
// Both standby policies are checked. A machine with a standby and one without
// differ by whether two optional records are on the wire at all, so a round trip
// that only ever carried one of them would leave the other's encoding unproven.
func checkRoundTrip() error {
	for _, hasStandby := range []bool{true, false} {
		if err := checkRoundTripFor(hasStandby); err != nil {
			return fmt.Errorf("has standby=%t: %w", hasStandby, err)
		}
	}
	return nil
}

func checkRoundTripFor(hasStandby bool) error {
	var builtStandby *builderdeployment.Instance
	var wantStandby *platformconfig.Instance
	var builtLease *builderdeployment.Lease
	var wantLease *platformconfig.Lease
	if hasStandby {
		builtStandby = &builderdeployment.Instance{
			EventsFile:           standbyEventsFile,
			StateFile:            standbyStateFile,
			APIAddress:           standbyAPIAddr,
			APIReadHeaderTimeout: standbyReadHeaderTimeout,
			APIShutdownTimeout:   standbyShutdownTimeout,
		}
		wantStandby = &platformconfig.Instance{
			EventsFile:           standbyEventsFile,
			StateFile:            standbyStateFile,
			APIAddress:           standbyAPIAddr,
			APIReadHeaderTimeout: standbyReadHeaderTimeout,
			APIShutdownTimeout:   standbyShutdownTimeout,
		}
		builtLease = &builderdeployment.Lease{
			File:                  leaseFile,
			Duration:              leaseDuration,
			RenewalInterval:       leaseRenewalInterval,
			HealthCheckInterval:   leaseHealthCheckInterval,
			FailbackStabilization: leaseFailbackStabilization,
			LagBound:              leaseLagBound,
		}
		wantLease = &platformconfig.Lease{
			File:                  leaseFile,
			Duration:              leaseDuration,
			RenewalInterval:       leaseRenewalInterval,
			HealthCheckInterval:   leaseHealthCheckInterval,
			FailbackStabilization: leaseFailbackStabilization,
			LagBound:              leaseLagBound,
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
		Primary: builderdeployment.Instance{
			EventsFile:           eventsFile,
			StateFile:            stateFile,
			APIAddress:           apiAddr,
			APIReadHeaderTimeout: readHeaderTimeout,
			APIShutdownTimeout:   shutdownTimeout,
		},
		Standby: builtStandby,
		Lease:   builtLease,
	}

	data, err := json.Marshal(built)
	if err != nil {
		return err
	}
	if err := checkWireShape(data, hasStandby); err != nil {
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
		Primary: platformconfig.Instance{
			EventsFile:           eventsFile,
			StateFile:            stateFile,
			APIAddress:           apiAddr,
			APIReadHeaderTimeout: readHeaderTimeout,
			APIShutdownTimeout:   shutdownTimeout,
		},
		Standby: wantStandby,
		Lease:   wantLease,
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("builder descriptor did not round-trip into the platform descriptor:\n  got:  %+v\n  want: %+v", got, want)
	}
	return nil
}

// checkWireShape checks the JSON the builder emits carries each instance record
// where the platform looks for it, complete, and carries no endpoint of its own.
//
// The checks are on the wire rather than on the decoded value because that is
// where the distinction exists: a standby the builder omitted and one it wrote as
// an empty object decode to different things, and only the bytes say which was
// produced.
func checkWireShape(data []byte, hasStandby bool) error {
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if err := checkWireInstance(wire, "primary", true); err != nil {
		return err
	}
	if err := checkWireInstance(wire, "standby", hasStandby); err != nil {
		return err
	}

	// The endpoints an instance binds and the files it owns belong to that
	// instance.
	for _, field := range []string{"events_file", "state_file", "api_address"} {
		if _, ok := wire[field]; ok {
			return fmt.Errorf("builder descriptor carries machine-level %q: endpoints and local files belong to an instance", field)
		}
	}
	return verifyWireLease(wire, hasStandby)
}

// checkWireInstance verifies one instance record is present exactly when the
// instance is deployed, and states everything that instance binds when it is.
func checkWireInstance(wire map[string]json.RawMessage, role string, deployed bool) error {
	raw, ok := wire[role]
	if !deployed {
		if ok {
			return fmt.Errorf("builder descriptor carries %s when that instance is not deployed", role)
		}
		return nil
	}
	if !ok {
		return fmt.Errorf("builder descriptor omitted %s", role)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for _, field := range []string{"events_file", "state_file", "api_address", "api_read_header_timeout", "api_shutdown_timeout"} {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("builder descriptor omitted %s.%s", role, field)
		}
	}
	return nil
}

func verifyWireLease(wire map[string]json.RawMessage, hasStandby bool) error {
	if !hasStandby {
		if _, ok := wire["lease"]; ok {
			return fmt.Errorf("builder descriptor carries lease when no standby is deployed")
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
	for _, field := range []string{"file", "duration", "renewal_interval", "health_check_interval", "failback_stabilization", "lag_bound"} {
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
