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
	site           = "north"
	machine        = "sensor"
	machineProfile = "sensor-node"
	machineIP      = "10.0.1.10"

	// The api addresses are on loopback. That is the contract: the platform API
	// is machine-local, so it is resolved onto 127.0.0.1 and never onto the
	// machine ip.
	// Each instance names every local file it owns outright, so a round trip that
	// dropped one, or resolved both instances onto the same path, would fail.
	// The machine's own store is in the fixture for the same reason and is not an
	// instance's: it is the file both instances append machine-scoped events to.
	machineEventsFile = "D:/opdl/customer-a/north/sensor/machine-events.jsonl"
	eventsFile        = "D:/opdl/customer-a/north/sensor/primary/events.jsonl"
	stateFile         = "D:/opdl/customer-a/north/sensor/primary/state.json"
	logFile           = "D:/opdl/customer-a/north/sensor/primary/platform.log"
	apiAddr           = "127.0.0.1:8080"
	standbyEventsFile = "D:/opdl/customer-a/north/sensor/standby/events.jsonl"
	standbyStateFile  = "D:/opdl/customer-a/north/sensor/standby/state.json"
	standbyLogFile    = "D:/opdl/customer-a/north/sensor/standby/platform.log"
	standbyAPIAddr    = "127.0.0.1:8081"

	// The listener timeouts are on the instance record for the same reason the
	// api address is: the two instances bind their own listeners. They differ
	// between the roles here so a round trip that swapped them would fail.
	readHeaderTimeout        = "5s"
	shutdownTimeout          = "10s"
	standbyReadHeaderTimeout = "6s"
	standbyShutdownTimeout   = "11s"

	// The embedded event fabric is on the instance record for the same reason
	// the api address is: each instance runs its own server on its own port.
	// Both roles carry one, and their addresses differ, so a round trip that
	// dropped the record or resolved both instances onto one server would fail.
	// The cluster name is the site's, and it is the one field the two share.
	//
	// The cluster addresses are on the machine ip rather than on loopback, which
	// is the one place the two differ from the api addresses above: the site's
	// cluster spans machines, so a member has to be reachable from another host.
	natsServerName         = "sensor-primary"
	natsClusterName        = "customer-a-north"
	natsClusterAddr        = machineIP + ":6222"
	standbyNATSServerName  = "sensor-standby"
	standbyNATSClusterAddr = machineIP + ":6223"

	// A member of the site on another machine. Its presence is what makes the
	// route lists below different from each other: a route list is per instance
	// and leaves that instance out, so a round trip that resolved one instance's
	// peers onto the other would fail.
	peerNATSRoute = "nats://10.0.1.11:6222"

	// The Primary Ownership lease a standby machine carries: a shared file and the
	// failover timings, round-tripped through the platform's type intact.
	leaseFile                  = "D:/opdl/customer-a/north/sensor/lease"
	leaseDuration              = "15s"
	leaseRenewalInterval       = "5s"
	leaseHealthCheckInterval   = "2s"
	leaseFailbackStabilization = "30s"

	// The services this machine hosts, and the probe policy the platform runs
	// against each. The two differ in port and in role so a round trip that
	// collapsed them, or resolved one service's policy onto the other, would
	// fail. The paths differ from each other for the same reason.
	sensorService     = "sensor-services"
	sensorServiceRole = "master"
	sensorProbePort   = 9101
	sensorProbePath   = "/health"
	coreService       = "core-services"
	coreServiceRole   = "slave"
	coreProbePort     = 9102
	coreProbePath     = "/healthz?deep=1"

	// One probe policy shared by both services. The freshness below is what it
	// derives: two intervals plus one timeout.
	probeType     = "http"
	probeInterval = "10s"
	probeTimeout  = "2s"
	probeRetries  = 3
	probeFreshFor = "22s"

	// A unit on another machine of the same site. It is in the fixture because
	// the remote half of the health contract is the half with no endpoint in it:
	// this entry proves identity, expected observers, and freshness survive the
	// round trip without a port or a path coming with them.
	peerMachine        = "gateway"
	peerMachineProfile = "gateway-node"
	peerService        = "gateway-services"
	peerServiceRole    = "master"
	peerFreshFor       = "32s"

	// The platform instance roles an inventory entry names as expected observers.
	// They are the same two words the instance records are keyed by, spelled here
	// as the values that cross the wire.
	observerPrimary = "primary"
	observerStandby = "standby"
)

// standbyNATSRoutes are the peers the Standby Instance's server dials: the other
// instance on its own machine, and the member on the other machine.
var standbyNATSRoutes = []string{"nats://" + natsClusterAddr, peerNATSRoute}

// builtServices and wantServices are this machine's hosted services as each
// module spells them. They are written out per module rather than converted,
// because a converter shared by both sides would round-trip its own bugs.
func builtServices() []builderdeployment.Service {
	return []builderdeployment.Service{
		{
			Name: sensorService,
			Role: sensorServiceRole,
			HealthCheck: builderdeployment.HealthCheck{
				Type: probeType, Port: sensorProbePort, Path: sensorProbePath,
				Interval: probeInterval, Timeout: probeTimeout, Retries: probeRetries,
			},
		},
		{
			Name: coreService,
			Role: coreServiceRole,
			HealthCheck: builderdeployment.HealthCheck{
				Type: probeType, Port: coreProbePort, Path: coreProbePath,
				Interval: probeInterval, Timeout: probeTimeout, Retries: probeRetries,
			},
		},
	}
}

func wantServices() []platformconfig.Service {
	return []platformconfig.Service{
		{
			Name: sensorService,
			Role: sensorServiceRole,
			HealthCheck: platformconfig.HealthCheck{
				Type: probeType, Port: sensorProbePort, Path: sensorProbePath,
				Interval: probeInterval, Timeout: probeTimeout, Retries: probeRetries,
			},
		},
		{
			Name: coreService,
			Role: coreServiceRole,
			HealthCheck: platformconfig.HealthCheck{
				Type: probeType, Port: coreProbePort, Path: coreProbePath,
				Interval: probeInterval, Timeout: probeTimeout, Retries: probeRetries,
			},
		},
	}
}

// siteObserverRoles are the platform instances expected to report on a unit this
// machine hosts. Both of a machine's instances probe every service on it, so the
// answer follows the machine's standby policy and nothing about the service.
func siteObserverRoles(hasStandby bool) []string {
	if hasStandby {
		return []string{observerPrimary, observerStandby}
	}
	return []string{observerPrimary}
}

// builtSiteServices and wantSiteServices are the site's static health inventory:
// this machine's two units, then a unit on another machine of the same site.
func builtSiteServices(hasStandby bool) []builderdeployment.SiteService {
	return []builderdeployment.SiteService{
		{
			Machine: machine, MachineProfile: machineProfile,
			Service: sensorService, ServiceRole: sensorServiceRole,
			ObserverRoles: siteObserverRoles(hasStandby), FreshFor: probeFreshFor,
		},
		{
			Machine: machine, MachineProfile: machineProfile,
			Service: coreService, ServiceRole: coreServiceRole,
			ObserverRoles: siteObserverRoles(hasStandby), FreshFor: probeFreshFor,
		},
		{
			Machine: peerMachine, MachineProfile: peerMachineProfile,
			Service: peerService, ServiceRole: peerServiceRole,
			ObserverRoles: []string{observerPrimary}, FreshFor: peerFreshFor,
		},
	}
}

func wantSiteServices(hasStandby bool) []platformconfig.SiteService {
	return []platformconfig.SiteService{
		{
			Machine: machine, MachineProfile: machineProfile,
			Service: sensorService, ServiceRole: sensorServiceRole,
			ObserverRoles: siteObserverRoles(hasStandby), FreshFor: probeFreshFor,
		},
		{
			Machine: machine, MachineProfile: machineProfile,
			Service: coreService, ServiceRole: coreServiceRole,
			ObserverRoles: siteObserverRoles(hasStandby), FreshFor: probeFreshFor,
		},
		{
			Machine: peerMachine, MachineProfile: peerMachineProfile,
			Service: peerService, ServiceRole: peerServiceRole,
			ObserverRoles: []string{observerPrimary}, FreshFor: peerFreshFor,
		},
	}
}

// primaryNATSRoutes are the peers the Primary Instance's server dials. They
// depend on the standby policy, because a standby that is not deployed runs no
// server and is nobody's peer.
func primaryNATSRoutes(hasStandby bool) []string {
	if hasStandby {
		return []string{"nats://" + standbyNATSClusterAddr, peerNATSRoute}
	}
	return []string{peerNATSRoute}
}

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
			LogFile:              standbyLogFile,
			APIAddress:           standbyAPIAddr,
			APIReadHeaderTimeout: standbyReadHeaderTimeout,
			APIShutdownTimeout:   standbyShutdownTimeout,
			NATS: builderdeployment.NATS{
				ServerName:     standbyNATSServerName,
				ClusterName:    natsClusterName,
				ClusterAddress: standbyNATSClusterAddr,
				Routes:         standbyNATSRoutes,
			},
		}
		wantStandby = &platformconfig.Instance{
			EventsFile:           standbyEventsFile,
			StateFile:            standbyStateFile,
			LogFile:              standbyLogFile,
			APIAddress:           standbyAPIAddr,
			APIReadHeaderTimeout: standbyReadHeaderTimeout,
			APIShutdownTimeout:   standbyShutdownTimeout,
			NATS: platformconfig.NATS{
				ServerName:     standbyNATSServerName,
				ClusterName:    natsClusterName,
				ClusterAddress: standbyNATSClusterAddr,
				Routes:         standbyNATSRoutes,
			},
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
		Platform:          "opdl",
		Project:           "customer-a",
		Environment:       "production",
		Site:              site,
		Machine:           machine,
		MachineProfile:    machineProfile,
		IP:                machineIP,
		Services:          builtServices(),
		SiteServices:      builtSiteServices(hasStandby),
		MachineEventsFile: machineEventsFile,
		Primary: builderdeployment.Instance{
			EventsFile:           eventsFile,
			StateFile:            stateFile,
			LogFile:              logFile,
			APIAddress:           apiAddr,
			APIReadHeaderTimeout: readHeaderTimeout,
			APIShutdownTimeout:   shutdownTimeout,
			NATS: builderdeployment.NATS{
				ServerName:     natsServerName,
				ClusterName:    natsClusterName,
				ClusterAddress: natsClusterAddr,
				Routes:         primaryNATSRoutes(hasStandby),
			},
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
		Platform:          "opdl",
		Project:           "customer-a",
		Environment:       "production",
		Site:              site,
		Machine:           machine,
		MachineProfile:    machineProfile,
		IP:                machineIP,
		Services:          wantServices(),
		SiteServices:      wantSiteServices(hasStandby),
		MachineEventsFile: machineEventsFile,
		Primary: platformconfig.Instance{
			EventsFile:           eventsFile,
			StateFile:            stateFile,
			LogFile:              logFile,
			APIAddress:           apiAddr,
			APIReadHeaderTimeout: readHeaderTimeout,
			APIShutdownTimeout:   shutdownTimeout,
			NATS: platformconfig.NATS{
				ServerName:     natsServerName,
				ClusterName:    natsClusterName,
				ClusterAddress: natsClusterAddr,
				Routes:         primaryNATSRoutes(hasStandby),
			},
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
	if err := checkWireInstance(wire, observerPrimary, true); err != nil {
		return err
	}
	if err := checkWireInstance(wire, observerStandby, hasStandby); err != nil {
		return err
	}

	// The endpoints an instance binds and the files it owns belong to that
	// instance.
	for _, field := range []string{"events_file", "state_file", "log_file", "api_address", "nats"} {
		if _, ok := wire[field]; ok {
			return fmt.Errorf("builder descriptor carries machine-level %q: endpoints and local files belong to an instance", field)
		}
	}
	// The machine's own store is the other way round: it is machine-level on
	// every machine, so a descriptor that omitted it, or put it on an instance,
	// would leave the machine with nowhere to keep its own account.
	if _, ok := wire["machine_events_file"]; !ok {
		return fmt.Errorf("builder descriptor omitted machine_events_file: the machine's shared event store is not an instance's file")
	}
	if err := checkWireHealth(wire); err != nil {
		return err
	}
	return verifyWireLease(wire, hasStandby)
}

// checkWireHealth checks the two halves of the health contract are on the wire
// and that only the local half carries an endpoint.
//
// The split is the point of the shape. A probe reaches a service on its own
// machine's ip, so the ports and paths belong to the descriptor of the machine
// hosting it and nowhere else. An inventory entry that carried them would hand
// every machine at the site a way to probe every other machine's services, which
// is a different feature with a different failure mode.
func checkWireHealth(wire map[string]json.RawMessage) error {
	services, ok := wire["services"]
	if !ok {
		return fmt.Errorf("builder descriptor omitted services: the platform probes what the machine hosts")
	}
	var hosted []map[string]json.RawMessage
	if err := json.Unmarshal(services, &hosted); err != nil {
		return fmt.Errorf("builder descriptor services is not a list of objects: %w", err)
	}
	for _, service := range hosted {
		for _, field := range []string{"name", "role", "health_check"} {
			if _, ok := service[field]; !ok {
				return fmt.Errorf("builder descriptor omitted services[].%s", field)
			}
		}
		var check map[string]json.RawMessage
		if err := json.Unmarshal(service["health_check"], &check); err != nil {
			return err
		}
		for _, field := range []string{"type", "port", "path", "interval", "timeout", "retries"} {
			if _, ok := check[field]; !ok {
				return fmt.Errorf("builder descriptor omitted services[].health_check.%s", field)
			}
		}
	}

	inventory, ok := wire["site_services"]
	if !ok {
		return fmt.Errorf("builder descriptor omitted site_services: every instance builds a view of the whole site")
	}
	var units []map[string]json.RawMessage
	if err := json.Unmarshal(inventory, &units); err != nil {
		return fmt.Errorf("builder descriptor site_services is not a list of objects: %w", err)
	}
	if len(units) == 0 {
		return fmt.Errorf("builder descriptor site_services is empty: it carries this machine's own services too")
	}
	for _, unit := range units {
		for _, field := range []string{"machine", "machine_profile", "service", "service_role", "observer_roles", "fresh_for"} {
			if _, ok := unit[field]; !ok {
				return fmt.Errorf("builder descriptor omitted site_services[].%s", field)
			}
		}
		for _, field := range []string{"health_check", "port", "path", "ip"} {
			if _, ok := unit[field]; ok {
				return fmt.Errorf("builder descriptor carries site_services[].%s: only the machine hosting a service probes it", field)
			}
		}
	}
	return nil
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
	for _, field := range []string{"events_file", "state_file", "log_file", "api_address", "api_read_header_timeout", "api_shutdown_timeout"} {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("builder descriptor omitted %s.%s", role, field)
		}
	}
	return checkWireNATS(fields, role)
}

// checkWireNATS verifies one instance's embedded event fabric record is on the
// wire and complete.
//
// It is nested rather than flat because the broker belongs to the instance, like
// its listener: a machine-level nats block would say the two processes share one
// broker, which they do not.
func checkWireNATS(fields map[string]json.RawMessage, role string) error {
	raw, ok := fields["nats"]
	if !ok {
		return fmt.Errorf("builder descriptor omitted %s.nats: every deployed instance runs its own embedded broker", role)
	}
	var nats map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nats); err != nil {
		return err
	}
	for _, field := range []string{"server_name", "cluster_name", "cluster_address"} {
		if _, ok := nats[field]; !ok {
			return fmt.Errorf("builder descriptor omitted %s.nats.%s", role, field)
		}
	}
	// The routes are checked separately because they are the one part of the
	// record that is legitimately absent: a site deploying a single instance has
	// no peer. The fixture does have peers, so on this wire they must be there.
	if _, ok := nats["routes"]; !ok {
		return fmt.Errorf("builder descriptor omitted %s.nats.routes: this fixture's instances have peers at their site", role)
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
