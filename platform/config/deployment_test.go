package config_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/config"
)

// The fixtures below are the minimal JSON that decodes: every field the platform
// requires to be stated, and nothing else. Tests remove or corrupt one part at a
// time rather than building a valid descriptor from scratch each time.
const (
	// machineJSON is the machine's own record: its identity, the shared event
	// store every machine states, and both halves of the health contract. The
	// decoder cross-checks the two halves against the identity, so they travel
	// together in one constant rather than being spelled out per case.
	//
	// Its inventory expects a report from the Primary Instance alone, which is
	// what a machine deploying no standby resolves. Fixtures that deploy one use
	// redundantMachineJSON.
	machineJSON = machineRecordJSON + `,` + siteServicesPrimaryJSON
	// redundantMachineJSON is the same record for a machine that deploys both
	// instances: both of them probe every service on it, so both are expected to
	// report on each unit.
	redundantMachineJSON = machineRecordJSON + `,` + siteServicesBothJSON
	// machineRecordJSON is the machine without its inventory: identity, event
	// store, and hosted services. Cases that state their own site_services build
	// from it, and machineRecordNoServicesJSON drops the local half too.
	machineRecordJSON           = machineRecordNoServicesJSON + `,` + servicesJSON
	machineRecordNoServicesJSON = machineIdentityJSON + `,` + machineEventsJSON
	machineIdentityJSON         = `"machine":"mock","machine_profile":"all-in-one"`
	machineEventsJSON           = `"machine_events_file":".data/platform/machine-events.jsonl"`
	// servicesJSON is the local half: one hosted service and the probe policy the
	// platform runs against it. Its freshness derives to 22s (2*10s + 2s).
	servicesJSON = `"services":[{"name":"core-services","role":"master","health_check":` +
		`{"type":"http","port":9101,"path":"/health","interval":"10s","timeout":"2s","retries":3}}]`
	// The remote half, naming this machine's own unit. The two differ only in who
	// is expected to report on it.
	siteServicesPrimaryJSON = `"site_services":[{` + siteUnitJSON + `,"observer_roles":["primary"],"fresh_for":"22s"}]`
	siteServicesBothJSON    = `"site_services":[{` + siteUnitJSON + `,"observer_roles":["primary","standby"],"fresh_for":"22s"}]`
	siteUnitJSON            = `"machine":"mock","machine_profile":"all-in-one","service":"core-services","service_role":"master"`
	primaryJSON             = `"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080",` + instanceExtrasJSON + `}`
	standbyJSON             = `"standby":{` + standbyFilesJSON + `,"api_address":"127.0.0.1:8081",` + standbyInstanceExtrasJSON + `}`
	// instanceExtrasJSON is what every instance record carries beyond its files
	// and its API address: the listener timeouts, and the embedded event fabric
	// broker the instance runs. Tests that remove one of these spell the rest
	// out inline instead.
	instanceExtrasJSON        = `"api_read_header_timeout":"5s","api_shutdown_timeout":"10s",` + natsJSON
	standbyInstanceExtrasJSON = `"api_read_header_timeout":"5s","api_shutdown_timeout":"10s",` + standbyNATSJSON
	// natsJSON is one instance's complete embedded event fabric record.
	natsJSON        = `"nats":{"server_name":"mock-primary","cluster_name":"local","cluster_address":"127.0.0.1:6222"}`
	standbyNATSJSON = `"nats":{"server_name":"mock-standby","cluster_name":"local","cluster_address":"127.0.0.1:6223"}`
	// The local files an instance owns. Each is stated on its own instance record,
	// so a descriptor missing either is a startup failure rather than an instance
	// that opens a path nothing authored.
	primaryFilesJSON = `"events_file":".data/platform/primary/events.jsonl","state_file":".data/platform/primary/state.json","log_file":".data/platform/primary/platform.log"`
	standbyFilesJSON = `"events_file":".data/platform/standby/events.jsonl","state_file":".data/platform/standby/state.json","log_file":".data/platform/standby/platform.log"`
	leaseJSON        = `"lease":{"file":"D:/opdl/lease",` + leaseTimingsJSON + `}`
	// leaseTimingsJSON are the timings a valid lease states, without the file.
	leaseTimingsJSON = `"duration":"15s","renewal_interval":"5s","health_check_interval":"2s","failback_stabilization":"30s"`

	// standbyless is a machine that deploys only a Primary Instance: no standby
	// record, and therefore no lease.
	standbyless = `{` + machineJSON + `,` + primaryJSON + `}`
	// redundant is a machine that deploys both instances and the lease they
	// contend for.
	redundant = `{` + redundantMachineJSON + `,` + primaryJSON + `,` + standbyJSON + `,` + leaseJSON + `}`
)

// TestDescriptorRequiresExplicitDecisions checks the descriptor decoder rejects
// JSON that leaves a decision to a zero value.
func TestDescriptorRequiresExplicitDecisions(t *testing.T) {
	tests := map[string]struct {
		json string
		err  string
	}{
		"missing primary": {json: `{` + machineJSON + `,` + standbyJSON + `,` + leaseJSON + `}`, err: "primary is required"},
		"null primary":    {json: `{` + machineJSON + `,"primary":null}`, err: "primary is required"},
		"missing primary events file": {
			json: `{` + machineJSON + `,"primary":{"state_file":".data/state.json","api_address":"127.0.0.1:8080",` + instanceExtrasJSON + `}}`,
			err:  "primary.events_file is required",
		},
		"missing primary state file": {
			json: `{` + machineJSON + `,"primary":{"events_file":".data/events.jsonl","log_file":".data/platform.log","api_address":"127.0.0.1:8080",` + instanceExtrasJSON + `}}`,
			err:  "primary.state_file is required",
		},
		"missing primary log file": {
			json: `{` + machineJSON + `,"primary":{"events_file":".data/events.jsonl","state_file":".data/state.json","api_address":"127.0.0.1:8080",` + instanceExtrasJSON + `}}`,
			err:  "primary.log_file is required",
		},
		"missing primary api address": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,` + instanceExtrasJSON + `}}`,
			err:  "primary.api_address is required",
		},
		"missing primary read header timeout": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_shutdown_timeout":"10s",` + natsJSON + `}}`,
			err:  "primary.api_read_header_timeout is required",
		},
		"bad primary shutdown timeout": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"soon",` + natsJSON + `}}`,
			err:  "primary.api_shutdown_timeout",
		},
		// The embedded broker is required on every deployed instance, and an
		// omitted field would decode as an unnamed server on no address rather
		// than as an omission, so each part is guarded on its own.
		"missing primary nats": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s"}}`,
			err:  "primary.nats is required",
		},
		"null primary nats": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s","nats":null}}`,
			err:  "primary.nats is required",
		},
		"missing primary nats server name": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s","nats":{"cluster_name":"local","cluster_address":"127.0.0.1:6222"}}}`,
			err:  "primary.nats.server_name is required",
		},
		"missing primary nats cluster name": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s","nats":{"server_name":"mock-primary","cluster_address":"127.0.0.1:6222"}}}`,
			err:  "primary.nats.cluster_name is required",
		},
		"missing primary nats cluster address": {
			json: `{` + machineJSON + `,"primary":{` + primaryFilesJSON + `,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s","nats":{"server_name":"mock-primary","cluster_name":"local"}}}`,
			err:  "primary.nats.cluster_address is required",
		},
		"missing standby nats": {
			json: `{` + machineJSON + `,` + primaryJSON + `,"standby":{` + standbyFilesJSON + `,"api_address":"127.0.0.1:8081","api_read_header_timeout":"5s","api_shutdown_timeout":"10s"},` + leaseJSON + `}`,
			err:  "standby.nats is required",
		},
		"missing standby events file": {
			json: `{` + machineJSON + `,` + primaryJSON + `,"standby":{"state_file":".data/standby/state.json","log_file":".data/standby/platform.log","api_address":"127.0.0.1:8081",` + instanceExtrasJSON + `},` + leaseJSON + `}`,
			err:  "standby.events_file is required",
		},
		"missing standby state file": {
			json: `{` + machineJSON + `,` + primaryJSON + `,"standby":{"events_file":".data/standby/events.jsonl","log_file":".data/standby/platform.log","api_address":"127.0.0.1:8081",` + instanceExtrasJSON + `},` + leaseJSON + `}`,
			err:  "standby.state_file is required",
		},
		"missing standby log file": {
			json: `{` + machineJSON + `,` + primaryJSON + `,"standby":{"events_file":".data/standby/events.jsonl","state_file":".data/standby/state.json","api_address":"127.0.0.1:8081",` + instanceExtrasJSON + `},` + leaseJSON + `}`,
			err:  "standby.log_file is required",
		},
		"lease without a standby": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + leaseJSON + `}`,
			err:  "lease is set but no standby is deployed",
		},
		"missing lease with a standby": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `}`,
			err:  "lease is required",
		},
		"null lease with a standby": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `,"lease":null}`,
			err:  "lease is required",
		},
		"missing lease file": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `,"lease":{` + leaseTimingsJSON + `}}`,
			err:  "lease.file is required",
		},
		"bad lease duration": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `,"lease":{"file":"D:/opdl/lease","duration":"soon","renewal_interval":"5s","health_check_interval":"2s","failback_stabilization":"30s"}}`,
			err:  "lease.duration",
		},
		"missing lease failback stabilization": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `,"lease":{"file":"D:/opdl/lease","duration":"15s","renewal_interval":"5s","health_check_interval":"2s"}}`,
			err:  "lease.failback_stabilization is required",
		},
		"missing machine events file": {
			json: `{` + primaryJSON + `}`,
			err:  "machine_events_file is required",
		},
		"null machine events file": {
			json: `{"machine_events_file":null,` + primaryJSON + `}`,
			err:  "machine_events_file is required",
		},
		// The health contract is guarded here for the same reason the lease
		// timings are: the runtime is about to schedule real requests against a
		// live service from these values, and a field that decoded as zero is a
		// descriptor that lost one rather than a default to fall back on.
		"missing services": {
			json: `{` + machineRecordNoServicesJSON + `,` + siteServicesPrimaryJSON + `,` + primaryJSON + `}`,
			err:  "services is required",
		},
		"missing site services": {
			json: `{` + machineRecordJSON + `,` + primaryJSON + `}`,
			err:  "site_services is required",
		},
		"unknown probe type": {
			json: `{` + machineIdentityJSON + `,` + machineEventsJSON + `,"services":[{"name":"core-services","role":"master",` +
				`"health_check":{"type":"ping","port":9101,"path":"/health","interval":"10s","timeout":"2s","retries":3}}],` +
				siteServicesPrimaryJSON + `,` + primaryJSON + `}`,
			err: `type "ping" is not a known probe`,
		},
		"padded service name": {
			json: `{` + machineIdentityJSON + `,` + machineEventsJSON + `,"services":[{"name":" core-services ","role":"master",` +
				`"health_check":{"type":"http","port":9101,"path":"/health","interval":"10s","timeout":"2s","retries":3}}],` +
				siteServicesPrimaryJSON + `,` + primaryJSON + `}`,
			err: "must not have leading or trailing whitespace",
		},
		"absolute probe url": {
			json: `{` + machineIdentityJSON + `,` + machineEventsJSON + `,"services":[{"name":"core-services","role":"master",` +
				`"health_check":{"type":"http","port":9101,"path":"http://127.0.0.1:9101/health","interval":"10s","timeout":"2s","retries":3}}],` +
				siteServicesPrimaryJSON + `,` + primaryJSON + `}`,
			err: "must not be an absolute URL",
		},
		"probe path with fragment": {
			json: `{` + machineIdentityJSON + `,` + machineEventsJSON + `,"services":[{"name":"core-services","role":"master",` +
				`"health_check":{"type":"http","port":9101,"path":"/health#ready","interval":"10s","timeout":"2s","retries":3}}],` +
				siteServicesPrimaryJSON + `,` + primaryJSON + `}`,
			err: "must not contain a fragment",
		},
		"probe path with invalid escape": {
			json: `{` + machineIdentityJSON + `,` + machineEventsJSON + `,"services":[{"name":"core-services","role":"master",` +
				`"health_check":{"type":"http","port":9101,"path":"/health%zz","interval":"10s","timeout":"2s","retries":3}}],` +
				siteServicesPrimaryJSON + `,` + primaryJSON + `}`,
			err: "is not a valid request path",
		},
		"probe timeout not shorter than interval": {
			json: `{` + machineIdentityJSON + `,` + machineEventsJSON + `,"services":[{"name":"core-services","role":"master",` +
				`"health_check":{"type":"http","port":9101,"path":"/health","interval":"10s","timeout":"10s","retries":3}}],` +
				siteServicesPrimaryJSON + `,` + primaryJSON + `}`,
			err: "must be shorter than interval",
		},
		"probe interval missing": {
			json: `{` + machineIdentityJSON + `,` + machineEventsJSON + `,"services":[{"name":"core-services","role":"master",` +
				`"health_check":{"type":"http","port":9101,"path":"/health","timeout":"2s","retries":3}}],` +
				siteServicesPrimaryJSON + `,` + primaryJSON + `}`,
			err: "interval is required",
		},
		"site unit observed by nobody": {
			json: `{` + machineRecordJSON + `,"site_services":[{` + siteUnitJSON + `,"observer_roles":[],"fresh_for":"22s"}],` + primaryJSON + `}`,
			err:  "observer_roles is required",
		},
		"site unit observed by standby alone": {
			json: `{` + machineRecordJSON + `,"site_services":[{` + siteUnitJSON + `,"observer_roles":["standby"],"fresh_for":"22s"}],` + primaryJSON + `}`,
			err:  "every machine deploys a primary and the order is fixed",
		},
		"hosted service missing from the inventory": {
			json: `{` + machineRecordJSON + `,"site_services":[{"machine":"other-machine","machine_profile":"all-in-one",` +
				`"service":"core-services","service_role":"master","observer_roles":["primary"],"fresh_for":"22s"}],` + primaryJSON + `}`,
			err: "has no site_services entry",
		},
		"extra local inventory unit": {
			json: `{` + machineRecordJSON + `,"site_services":[{` + siteUnitJSON +
				`,"observer_roles":["primary"],"fresh_for":"22s"},{"machine":"mock","machine_profile":"all-in-one",` +
				`"service":"other-services","service_role":"master","observer_roles":["primary"],"fresh_for":"22s"}],` + primaryJSON + `}`,
			err: "names a service this machine does not host",
		},
		"freshness differs from the probe policy": {
			json: `{` + machineRecordJSON + `,"site_services":[{` + siteUnitJSON + `,"observer_roles":["primary"],"fresh_for":"21s"}],` + primaryJSON + `}`,
			err:  "is not the 22s",
		},
		"freshness longer than the probe policy": {
			json: `{` + machineRecordJSON + `,"site_services":[{` + siteUnitJSON + `,"observer_roles":["primary"],"fresh_for":"23s"}],` + primaryJSON + `}`,
			err:  "is not the 22s",
		},
		"remote units disagree about their machine profile": {
			json: `{` + machineRecordJSON + `,"site_services":[{` + siteUnitJSON +
				`,"observer_roles":["primary"],"fresh_for":"22s"},` +
				`{"machine":"gateway","machine_profile":"gateway-node","service":"gateway-a","service_role":"master","observer_roles":["primary"],"fresh_for":"22s"},` +
				`{"machine":"gateway","machine_profile":"other-node","service":"gateway-b","service_role":"slave","observer_roles":["primary"],"fresh_for":"22s"}],` +
				primaryJSON + `}`,
			err: "disagrees with another service",
		},
		"service probe uses the primary api port": {
			json: `{` + machineIdentityJSON + `,` + machineEventsJSON + `,"services":[{"name":"core-services","role":"master",` +
				`"health_check":{"type":"http","port":8080,"path":"/health","interval":"10s","timeout":"2s","retries":3}}],` +
				siteServicesPrimaryJSON + `,` + primaryJSON + `}`,
			err: "a service cannot be probed on a port the platform binds",
		},
		"two services claim one probe endpoint": {
			json: `{` + machineIdentityJSON + `,` + machineEventsJSON + `,"services":[` +
				`{"name":"core-services","role":"master","health_check":{"type":"http","port":9101,"path":"/health","interval":"10s","timeout":"2s","retries":3}},` +
				`{"name":"other-services","role":"slave","health_check":{"type":"http","port":9101,"path":"/health","interval":"10s","timeout":"2s","retries":3}}],` +
				`"site_services":[{` + siteUnitJSON + `,"observer_roles":["primary"],"fresh_for":"22s"},` +
				`{"machine":"mock","machine_profile":"all-in-one","service":"other-services","service_role":"slave","observer_roles":["primary"],"fresh_for":"22s"}],` +
				primaryJSON + `}`,
			err: "two services may share a port but not an endpoint",
		},
		"inventory expects one observer from a machine deploying two": {
			json: `{` + machineJSON + `,` + primaryJSON + `,` + standbyJSON + `,` + leaseJSON + `}`,
			err:  "both of a machine's instances probe every service on it",
		},

		"standby-less machine": {json: standbyless},
		"redundant machine":    {json: redundant},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var descriptor config.Descriptor
			err := json.Unmarshal([]byte(test.json), &descriptor)
			if test.err != "" {
				require.ErrorContains(t, err, test.err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestDescriptorAllowsServicesToShareAProbePort(t *testing.T) {
	const shared = `{` + machineIdentityJSON + `,` + machineEventsJSON + `,"services":[` +
		`{"name":"core-services","role":"master","health_check":{"type":"http","port":9101,"path":"/core/health","interval":"10s","timeout":"2s","retries":3}},` +
		`{"name":"other-services","role":"slave","health_check":{"type":"http","port":9101,"path":"/other/health","interval":"10s","timeout":"2s","retries":3}}],` +
		`"site_services":[` +
		`{"machine":"mock","machine_profile":"all-in-one","service":"core-services","service_role":"master","observer_roles":["primary"],"fresh_for":"22s"},` +
		`{"machine":"mock","machine_profile":"all-in-one","service":"other-services","service_role":"slave","observer_roles":["primary"],"fresh_for":"22s"}],` +
		primaryJSON + `}`

	var descriptor config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(shared), &descriptor))
}

// TestDescriptorDecodesAStandbylessMachine checks the shape a machine with no
// redundancy decodes into: a primary record, and nothing where the peer and the
// lease would be.
func TestDescriptorDecodesAStandbylessMachine(t *testing.T) {
	var descriptor config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(standbyless), &descriptor))

	require.Equal(t, "127.0.0.1:8080", descriptor.Primary.APIAddress)
	require.False(t, descriptor.HasStandby())
	require.Nil(t, descriptor.Standby)
	require.Nil(t, descriptor.Lease)
}

// TestDescriptorDecodesARedundantMachine is the counterpart: a standby that is
// deployed decodes with its own endpoints and the lease the two instances
// contend for.
func TestDescriptorDecodesARedundantMachine(t *testing.T) {
	var descriptor config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(redundant), &descriptor))

	require.True(t, descriptor.HasStandby())
	require.Equal(t, "127.0.0.1:8081", descriptor.Standby.APIAddress)
	require.NotNil(t, descriptor.Lease)
	require.Equal(t, "30s", descriptor.Lease.FailbackStabilization)
}

// TestDescriptorDecodesTheEventFabricRoutes checks the peers an instance's
// embedded server dials survive the descriptor, and that having none decodes as
// having none.
//
// The routes are the one part of the fabric record with no required check on
// them, because a site that deploys a single instance correctly carries an empty
// list. That makes it the part worth decoding explicitly: a truncated list is
// indistinguishable from a lone instance, so this pins what each shape means.
func TestDescriptorDecodesTheEventFabricRoutes(t *testing.T) {
	const routed = `{` + machineJSON + `,"primary":{` + primaryFilesJSON +
		`,"api_address":"127.0.0.1:8080","api_read_header_timeout":"5s","api_shutdown_timeout":"10s"` +
		`,"nats":{"server_name":"mock-primary","cluster_name":"local","cluster_address":"10.0.1.10:6222"` +
		`,"routes":["nats://10.0.1.10:6223","nats://10.0.1.11:6222"]}}}`

	var descriptor config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(routed), &descriptor))
	require.Equal(t, "10.0.1.10:6222", descriptor.Primary.NATS.ClusterAddress,
		"the cluster address is on the machine's ip, not on loopback: the site's cluster spans machines")
	require.Equal(t, []string{"nats://10.0.1.10:6223", "nats://10.0.1.11:6222"}, descriptor.Primary.NATS.Routes)

	// The mock descriptor is a site of one instance, so it carries no routes at
	// all and is still a complete record.
	var alone config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(standbyless), &alone))
	require.Empty(t, alone.Primary.NATS.Routes)
}

// TestDescriptorInstanceSelectsByRole checks the accessor the runtime uses to
// find its own record and its peer's, including the peer that does not exist.
func TestDescriptorInstanceSelectsByRole(t *testing.T) {
	var redundantMachine config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(redundant), &redundantMachine))
	require.Equal(t, "127.0.0.1:8080", redundantMachine.Instance(config.RolePrimary).APIAddress)
	require.Equal(t, "127.0.0.1:8081", redundantMachine.Instance(config.RoleStandby).APIAddress)

	var alone config.Descriptor
	require.NoError(t, json.Unmarshal([]byte(standbyless), &alone))
	require.Empty(t, alone.Instance(config.RoleStandby).APIAddress,
		"an instance the machine does not deploy has no address to read")

	require.Equal(t, config.RolePrimary, config.Role(false))
	require.Equal(t, config.RoleStandby, config.Role(true))
}
