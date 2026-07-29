package deployment_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
)

// The fixture is a machine that deploys both instances, which is the shape the
// rules are about: two independent runtimes on one host, each with its own
// service, local files, and listener.
const (
	machineIP = "10.0.1.10"

	// The api addresses are on loopback. The platform API is machine-local, and
	// that is what the descriptor is checked against.
	// The machine's own store is not an instance's file: both instances append
	// their machine-scoped events to it.
	machineEventsFile = "D:/opdl-data/sensor/machine-events.jsonl"
	primaryAPI        = "127.0.0.1:8080"
	primaryEventsFile = "D:/opdl-data/sensor/primary/events.jsonl"
	primaryStateFile  = "D:/opdl-data/sensor/primary/state.json"
	primaryLogFile    = "D:/opdl-data/sensor/primary/platform.log"
	standbyAPI        = "127.0.0.1:8081"
	standbyEventsFile = "D:/opdl-data/sensor/standby/events.jsonl"
	standbyStateFile  = "D:/opdl-data/sensor/standby/state.json"
	standbyLogFile    = "D:/opdl-data/sensor/standby/platform.log"

	// Each instance's embedded event fabric server: its own identity, its own
	// route listener on the machine's ip, and the one cluster every server at the
	// site belongs to. The route listeners are the only endpoints in a descriptor
	// that are not on loopback, because the site's cluster spans machines.
	natsCluster        = "customer-a-north"
	primaryNATSServer  = "sensor-primary"
	primaryNATSCluster = machineIP + ":6222"
	standbyNATSServer  = "sensor-standby"
	standbyNATSCluster = machineIP + ":6223"

	// The peer at the other machine of the site. Its address is on a different
	// machine's ip, which is what a route is: the two instances here route to
	// each other and to it.
	peerNATSRoute = "nats://10.0.1.11:6222"
)

func validDescriptor() deployment.Descriptor {
	return deployment.Descriptor{
		Platform:       "opdl",
		Project:        "customer-a",
		Environment:    "production",
		Site:           "north",
		Machine:        "sensor",
		MachineProfile: "sensor-node",
		IP:             machineIP,
		Services: []deployment.Service{{
			Name: "sensor-services",
			Role: "master",
			HealthCheck: deployment.HealthCheck{
				Type:     "http",
				Port:     9101,
				Path:     "/health",
				Interval: "10s",
				Timeout:  "2s",
				Retries:  3,
			},
		}},
		SiteServices: []deployment.SiteService{{
			Machine:        "sensor",
			MachineProfile: "sensor-node",
			Service:        "sensor-services",
			ServiceRole:    "master",
			ObserverRoles:  []string{"primary", "standby"},
			FreshFor:       "22s",
		}},
		MachineEventsFile: machineEventsFile,
		Primary: deployment.Instance{
			Service:              &deployment.WinService{Name: "sensor-primary", DisplayName: "sensor primary"},
			EventsFile:           primaryEventsFile,
			StateFile:            primaryStateFile,
			LogFile:              primaryLogFile,
			APIAddress:           primaryAPI,
			APIReadHeaderTimeout: "5s",
			APIShutdownTimeout:   "10s",
			NATS: deployment.NATS{
				ServerName:     primaryNATSServer,
				ClusterName:    natsCluster,
				ClusterAddress: primaryNATSCluster,
				Routes:         []string{"nats://" + standbyNATSCluster, peerNATSRoute},
			},
		},
		Standby: &deployment.Instance{
			Service:              &deployment.WinService{Name: "sensor-standby", DisplayName: "sensor standby"},
			EventsFile:           standbyEventsFile,
			StateFile:            standbyStateFile,
			LogFile:              standbyLogFile,
			APIAddress:           standbyAPI,
			APIReadHeaderTimeout: "5s",
			APIShutdownTimeout:   "10s",
			NATS: deployment.NATS{
				ServerName:     standbyNATSServer,
				ClusterName:    natsCluster,
				ClusterAddress: standbyNATSCluster,
				Routes:         []string{"nats://" + primaryNATSCluster, peerNATSRoute},
			},
		},
		Lease: &deployment.Lease{
			File:                  "D:/opdl-data/sensor/lease",
			Duration:              "15s",
			RenewalInterval:       "5s",
			HealthCheckInterval:   "2s",
			FailbackStabilization: "30s",
		},
	}
}

func TestDescriptorValidateOK(t *testing.T) {
	require.NoError(t, validDescriptor().Validate())
}

func TestDescriptorValidateAllowsServicesToShareAProbePort(t *testing.T) {
	d := validDescriptor()
	service := d.Services[0]
	service.Name = "other-services"
	service.HealthCheck.Path = "/other/health"
	d.Services = append(d.Services, service)
	unit := d.SiteServices[0]
	unit.Service = service.Name
	d.SiteServices = append(d.SiteServices, unit)

	require.NoError(t, d.Validate())
}

func TestDescriptorValidateAllowsSameServiceNameInDifferentRoles(t *testing.T) {
	d := validDescriptor()
	slaveService := d.Services[0]
	slaveService.Role = "slave"
	slaveService.HealthCheck.Path = "/slave-health"
	d.Services = append(d.Services, slaveService)

	slaveUnit := d.SiteServices[0]
	slaveUnit.ServiceRole = "slave"
	d.SiteServices = append(d.SiteServices, slaveUnit)

	require.NoError(t, d.Validate())
}

func TestDescriptorValidateFailures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*deployment.Descriptor)
		errText string
	}{
		{"missing platform", func(d *deployment.Descriptor) { d.Platform = "" }, "platform is required"},
		{"missing project", func(d *deployment.Descriptor) { d.Project = "" }, "project is required"},
		{"missing environment", func(d *deployment.Descriptor) { d.Environment = "" }, "environment is required"},
		{"missing site", func(d *deployment.Descriptor) { d.Site = "" }, "site is required"},
		{"missing machine", func(d *deployment.Descriptor) { d.Machine = "" }, "machine is required"},
		{"missing machine profile", func(d *deployment.Descriptor) { d.MachineProfile = "" }, "machine profile is required"},
		{"invalid ip", func(d *deployment.Descriptor) { d.IP = "not-an-ip" }, "is not a valid IP address"},
		{"no services", func(d *deployment.Descriptor) { d.Services = nil }, "at least one service is required"},
		{
			"unknown service role",
			func(d *deployment.Descriptor) { d.Services[0].Role = "leader" },
			`role "leader" is not a known role`,
		},
		{
			"padded service name",
			func(d *deployment.Descriptor) { d.Services[0].Name = " sensor-services " },
			"must not have leading or trailing whitespace",
		},
		{
			"duplicate service",
			func(d *deployment.Descriptor) { d.Services = append(d.Services, d.Services[0]) },
			"is hosted more than once",
		},
		{
			"unknown probe type",
			func(d *deployment.Descriptor) { d.Services[0].HealthCheck.Type = "ping" },
			`type "ping" is not a known probe`,
		},
		{
			"probe port out of range",
			func(d *deployment.Descriptor) { d.Services[0].HealthCheck.Port = 0 },
			"port 0 is out of range",
		},
		{
			"relative probe path",
			func(d *deployment.Descriptor) { d.Services[0].HealthCheck.Path = "health" },
			`path "health" must start with "/"`,
		},
		{
			"absolute probe url",
			func(d *deployment.Descriptor) { d.Services[0].HealthCheck.Path = "http://10.0.1.10:9101/health" },
			"must not be an absolute URL",
		},
		{
			"probe path with fragment",
			func(d *deployment.Descriptor) { d.Services[0].HealthCheck.Path = "/health#ready" },
			"must not contain a fragment",
		},
		{
			"probe path with invalid escape",
			func(d *deployment.Descriptor) { d.Services[0].HealthCheck.Path = "/health%zz" },
			"is not a valid request path",
		},
		{
			"probe timeout not shorter than interval",
			func(d *deployment.Descriptor) { d.Services[0].HealthCheck.Timeout = "10s" },
			"must be shorter than interval",
		},
		{
			"derived freshness overflows",
			func(d *deployment.Descriptor) {
				d.Services[0].HealthCheck.Interval = "2562047h47m16.854775807s"
				d.Services[0].HealthCheck.Timeout = "1ns"
			},
			"overflows a duration",
		},
		{
			"probe retries below one",
			func(d *deployment.Descriptor) { d.Services[0].HealthCheck.Retries = 0 },
			"retries must be at least 1",
		},
		{
			"no site inventory",
			func(d *deployment.Descriptor) { d.SiteServices = nil },
			"site_services is required",
		},
		{
			"site unit listed twice",
			func(d *deployment.Descriptor) { d.SiteServices = append(d.SiteServices, d.SiteServices[0]) },
			"is listed more than once",
		},
		{
			"site unit with no observers",
			func(d *deployment.Descriptor) { d.SiteServices[0].ObserverRoles = nil },
			"observer_roles is required",
		},
		{
			"site unit observed by standby alone",
			func(d *deployment.Descriptor) { d.SiteServices[0].ObserverRoles = []string{"standby"} },
			"every machine deploys a primary and the order is fixed",
		},
		{
			"hosted service missing from the inventory",
			func(d *deployment.Descriptor) { d.SiteServices[0].Machine = "other-machine" },
			"has no site_services entry",
		},
		{
			"extra local inventory unit",
			func(d *deployment.Descriptor) {
				unit := d.SiteServices[0]
				unit.Service = "other-services"
				d.SiteServices = append(d.SiteServices, unit)
			},
			"names a service this machine does not host",
		},
		{
			"inventory disagrees about the service role",
			func(d *deployment.Descriptor) { d.SiteServices[0].ServiceRole = "slave" },
			"one service plays one part",
		},
		{
			"inventory expects no standby report from a machine that deploys one",
			func(d *deployment.Descriptor) { d.SiteServices[0].ObserverRoles = []string{"primary"} },
			"both of a machine's instances probe every service on it",
		},
		{
			"freshness differs from the probe policy",
			func(d *deployment.Descriptor) { d.SiteServices[0].FreshFor = "21s" },
			"is not the 22s",
		},
		{
			"freshness longer than the probe policy",
			func(d *deployment.Descriptor) { d.SiteServices[0].FreshFor = "23s" },
			"is not the 22s",
		},
		{
			"remote units disagree about their machine profile",
			func(d *deployment.Descriptor) {
				d.SiteServices = append(d.SiteServices,
					deployment.SiteService{
						Machine: "gateway", MachineProfile: "gateway-node", Service: "gateway-a", ServiceRole: "master",
						ObserverRoles: []string{"primary"}, FreshFor: "22s",
					},
					deployment.SiteService{
						Machine: "gateway", MachineProfile: "other-node", Service: "gateway-b", ServiceRole: "slave",
						ObserverRoles: []string{"primary"}, FreshFor: "22s",
					},
				)
			},
			"disagrees with another service",
		},
		{
			"service probe uses the primary api port",
			func(d *deployment.Descriptor) { d.Services[0].HealthCheck.Port = 8080 },
			"a service cannot be probed on a port the platform binds",
		},
		{
			"service probe uses a non-canonical primary api port",
			func(d *deployment.Descriptor) {
				d.Primary.APIAddress = "127.0.0.1:09101"
			},
			"a service cannot be probed on a port the platform binds",
		},
		{
			"two services claim one probe endpoint",
			func(d *deployment.Descriptor) {
				service := d.Services[0]
				service.Name = "other-services"
				d.Services = append(d.Services, service)
				unit := d.SiteServices[0]
				unit.Service = service.Name
				d.SiteServices = append(d.SiteServices, unit)
			},
			"two services may share a port but not an endpoint",
		},
		{
			"missing machine events file",
			func(d *deployment.Descriptor) { d.MachineEventsFile = "" },
			"machine_events_file is required",
		},
		{"lease set while no standby is deployed", func(d *deployment.Descriptor) { d.Standby = nil }, "omit lease when standby is absent"},
		{"missing lease while a standby is deployed", func(d *deployment.Descriptor) { d.Lease = nil }, "lease is required when a standby is deployed"},

		// Services.
		{"missing primary service", func(d *deployment.Descriptor) { d.Primary.Service = nil }, "primary.service is required"},
		{"missing primary service name", func(d *deployment.Descriptor) { d.Primary.Service.Name = "" }, "primary.service.name is required"},
		{"missing standby service", func(d *deployment.Descriptor) { d.Standby.Service = nil }, "standby.service is required"},
		{"missing standby service name", func(d *deployment.Descriptor) { d.Standby.Service.Name = "" }, "standby.service.name is required"},
		{"colliding service names", func(d *deployment.Descriptor) { d.Standby.Service.Name = d.Primary.Service.Name }, "share service name"},

		// Per-instance endpoints and local files.
		{"missing primary api address", func(d *deployment.Descriptor) { d.Primary.APIAddress = "" }, "primary.api_address is required"},
		{"missing primary events file", func(d *deployment.Descriptor) { d.Primary.EventsFile = "" }, "primary.events_file is required"},
		{"missing primary state file", func(d *deployment.Descriptor) { d.Primary.StateFile = "" }, "primary.state_file is required"},
		{"missing primary log file", func(d *deployment.Descriptor) { d.Primary.LogFile = "" }, "primary.log_file is required"},
		{"missing standby api address", func(d *deployment.Descriptor) { d.Standby.APIAddress = "" }, "standby.api_address is required"},
		{"missing standby events file", func(d *deployment.Descriptor) { d.Standby.EventsFile = "" }, "standby.events_file is required"},
		{"missing standby state file", func(d *deployment.Descriptor) { d.Standby.StateFile = "" }, "standby.state_file is required"},
		{"missing standby log file", func(d *deployment.Descriptor) { d.Standby.LogFile = "" }, "standby.log_file is required"},
		{
			"one instance points both its files at one path",
			func(d *deployment.Descriptor) { d.Primary.StateFile = d.Primary.EventsFile },
			"primary.events_file and primary.state_file are both",
		},
		{
			"instances share an events file",
			func(d *deployment.Descriptor) { d.Standby.EventsFile = d.Primary.EventsFile },
			"primary.events_file and standby.events_file are both",
		},
		{
			"instances share a log file",
			func(d *deployment.Descriptor) { d.Standby.LogFile = d.Primary.LogFile },
			"primary.log_file and standby.log_file are both",
		},
		{
			"an instance logs into its own events file",
			func(d *deployment.Descriptor) { d.Primary.LogFile = d.Primary.EventsFile },
			"primary.events_file and primary.log_file are both",
		},
		{
			"instances share a state file",
			func(d *deployment.Descriptor) { d.Standby.StateFile = d.Primary.StateFile },
			"primary.state_file and standby.state_file are both",
		},
		{
			// This repo is Windows-only, so two spellings of one path are one file
			// and comparing them literally would let both instances open it.
			"instances share a state file spelled differently",
			func(d *deployment.Descriptor) { d.Standby.StateFile = `D:\OPDL-DATA\sensor\primary\STATE.JSON` },
			"primary.state_file and standby.state_file are both",
		},
		{
			// The machine's store is the machine's account of itself. Pointed at an
			// instance's record it would be lost the moment that instance stopped
			// being the one that owns the machine.
			"machine store points at an instance's events file",
			func(d *deployment.Descriptor) { d.MachineEventsFile = d.Standby.EventsFile },
			"machine_events_file and standby.events_file are both",
		},
		{
			"machine store points at the lease file",
			func(d *deployment.Descriptor) { d.MachineEventsFile = d.Lease.File },
			"machine_events_file and lease.file are both",
		},
		{
			"api address off loopback",
			func(d *deployment.Descriptor) { d.Primary.APIAddress = "10.0.1.10:8080" },
			"is not on the loopback interface",
		},
		{
			"missing primary read header timeout",
			func(d *deployment.Descriptor) { d.Primary.APIReadHeaderTimeout = "" },
			"primary.api_read_header_timeout is required",
		},
		{
			"non-positive primary read header timeout",
			func(d *deployment.Descriptor) { d.Primary.APIReadHeaderTimeout = "0s" },
			"must be positive",
		},
		{
			"missing standby shutdown timeout",
			func(d *deployment.Descriptor) { d.Standby.APIShutdownTimeout = "" },
			"standby.api_shutdown_timeout is required",
		},
		{
			"missing lease failback stabilization",
			func(d *deployment.Descriptor) { d.Lease.FailbackStabilization = "" },
			"lease.failback_stabilization is required",
		},
		{
			"instances share an api address",
			func(d *deployment.Descriptor) { d.Standby.APIAddress = d.Primary.APIAddress },
			"cannot share one",
		},
		// Each instance binds two listeners, so the rule is about every listener
		// on the machine rather than about the two APIs: a server resolved onto
		// its own instance's API port is the same failure to bind.
		{
			"instances share a nats cluster address",
			func(d *deployment.Descriptor) {
				d.Standby.NATS.ClusterAddress = d.Primary.NATS.ClusterAddress
				// The standby routed to the primary, which it now is, so the
				// route goes with the address. What is left is the collision.
				d.Standby.NATS.Routes = []string{peerNATSRoute}
			},
			"cannot share one",
		},
		// The two are on different interfaces now — the API on loopback and the
		// route listener on the machine's ip — so the check is on the port. A
		// machine whose ip is 127.0.0.1, which is every scenario and every
		// developer's box, would otherwise resolve two listeners onto one socket
		// and be told nothing.
		{
			"an instance's server takes its own api port",
			func(d *deployment.Descriptor) { d.Primary.NATS.ClusterAddress = machineIP + ":8080" },
			"cannot share one",
		},
		{
			"missing primary nats server name",
			func(d *deployment.Descriptor) { d.Primary.NATS.ServerName = "" },
			"primary.nats.server_name is required",
		},
		{
			"missing standby nats cluster name",
			func(d *deployment.Descriptor) { d.Standby.NATS.ClusterName = "" },
			"standby.nats.cluster_name is required",
		},
		{
			"missing primary nats cluster address",
			func(d *deployment.Descriptor) { d.Primary.NATS.ClusterAddress = "" },
			"primary.nats.cluster_address is required",
		},
		// A route listener on loopback is reachable only from the machine that
		// binds it, which is a site cluster that can never have a second member.
		{
			"nats cluster address on loopback",
			func(d *deployment.Descriptor) { d.Primary.NATS.ClusterAddress = "127.0.0.1:6222" },
			`is not on the machine's ip "10.0.1.10"`,
		},
		{
			"nats cluster address on another machine's ip",
			func(d *deployment.Descriptor) { d.Primary.NATS.ClusterAddress = "10.0.1.11:6222" },
			`is not on the machine's ip "10.0.1.10"`,
		},
		{
			"a route is not a url",
			func(d *deployment.Descriptor) { d.Primary.NATS.Routes = []string{"nats://%zz"} },
			"primary.nats.routes[0]: route \"nats://%zz\" is not a URL",
		},
		{
			"a route is a bare address",
			func(d *deployment.Descriptor) { d.Primary.NATS.Routes = []string{"10.0.1.11:6222"} },
			"primary.nats.routes[0]",
		},
		{
			"a route is dialed as something other than a route",
			func(d *deployment.Descriptor) { d.Primary.NATS.Routes = []string{"tcp://10.0.1.11:6222"} },
			"must use the nats:// scheme",
		},
		{
			"a route has no port",
			func(d *deployment.Descriptor) { d.Primary.NATS.Routes = []string{"nats://10.0.1.11"} },
			"must be host:port",
		},
		{
			"a route points at this instance's own listener",
			func(d *deployment.Descriptor) {
				d.Primary.NATS.Routes = []string{"nats://" + primaryNATSCluster}
			},
			"is this instance's own cluster address",
		},
		{
			"a peer is routed to twice",
			func(d *deployment.Descriptor) {
				d.Primary.NATS.Routes = []string{peerNATSRoute, peerNATSRoute}
			},
			"each peer is routed to once",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := validDescriptor()
			tc.mutate(&d)
			require.ErrorContains(t, d.Validate(), tc.errText)
		})
	}
}

// TestDescriptorValidateAcceptsOneInstanceMachine checks a machine that deploys
// no standby is a valid deployment: one instance that binds its API, no peer,
// and no lease to contend for.
func TestDescriptorValidateAcceptsOneInstanceMachine(t *testing.T) {
	d := validDescriptor()
	d.Standby = nil
	d.Lease = nil
	// A machine with one instance has one observer for every service on it, and
	// the inventory says so. Nobody is expected to report and missing.
	d.SiteServices[0].ObserverRoles = []string{"primary"}
	d.Primary.NATS.Routes = []string{peerNATSRoute}
	require.NoError(t, d.Validate())
}

// TestDescriptorValidateAcceptsASiteOfOneInstance checks the one member with
// nobody to route to.
//
// An empty route list is the correct resolution for a site that deploys a single
// instance, not a truncated one. The instance still runs its own server, so the
// fabric it serves health checks about is there; there is simply no peer.
func TestDescriptorValidateAcceptsASiteOfOneInstance(t *testing.T) {
	d := validDescriptor()
	d.Standby = nil
	d.Lease = nil
	d.SiteServices[0].ObserverRoles = []string{"primary"}
	d.Primary.NATS.Routes = nil
	require.NoError(t, d.Validate())
}
