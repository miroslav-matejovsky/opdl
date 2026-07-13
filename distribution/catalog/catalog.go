package catalog

import "slices"

// Feature keys are the switches a distribution can turn on for a machine. They
// gate which service kinds may be assigned and which optional behavior the
// platform activates at runtime.
const (
	FeatureOPCUA     = "opcua"
	FeatureAlarms    = "alarms"
	FeatureRecording = "recording"
	FeatureAnalytics = "analytics"
	FeatureHistorian = "historian"
	// FeatureChaos arms the kernel's chaos subsystem. Unlike the other
	// features it gates a runtime subsystem rather than a hostable service, so
	// it has no service catalog entry and no factory. It is the first feature
	// whose only effect is to activate a kernel subsystem.
	FeatureChaos = "chaos"
)

// Service kinds are the hostable units of platform behavior. Every kind is a
// generic platform service backed by a runtime factory; a project selects and
// configures kinds per machine but never invents one. New kinds are added by
// the platform, alongside their implementation.
const (
	ServiceSensor       = "sensor-service"
	ServiceOPCUAAdapter = "opcua-adapter"
	ServiceHistorian    = "historian"
	ServiceAlarm        = "alarm-service"
	ServiceIntegration  = "integration-service"
)

// Service describes one hostable service kind. RequiredFeature, when non-empty,
// is the feature that must be enabled on a machine before the service may be
// assigned to it; this is how the model gates capabilities.
type Service struct {
	// Name is the service kind identifier used in blueprints and descriptors.
	Name string
	// Description is a short human-readable summary of the service.
	Description string
	// RequiredFeature gates assignment: empty means always assignable,
	// otherwise the named feature must be enabled on the machine.
	RequiredFeature string
	// Capabilities is fixed metadata for this kind. Builder-side tooling may use
	// it to wire a static fleet map; runtime services never advertise it.
	Capabilities Capabilities
}

// Capabilities is static catalog metadata for one service kind.
type Capabilities struct {
	Provides  []string
	Publishes []string
	Consumes  []string
	Handles   []string
}

// services is the catalog of hostable service kinds: closed to projects,
// extended by the platform alongside each kind's runtime factory.
var services = []Service{
	{
		Name:         ServiceSensor,
		Description:  "Acquires sensor readings and publishes them as a snapshot+delta object stream.",
		Capabilities: Capabilities{Publishes: []string{"sensor.snapshot", "sensor.delta"}, Handles: []string{"telemetry.sensors.resync"}},
	},
	{
		Name:            ServiceOPCUAAdapter,
		Description:     "Ingests OPC UA nodes, maps them to platform sensor contracts, and publishes them.",
		RequiredFeature: FeatureOPCUA,
		Capabilities:    Capabilities{Publishes: []string{"sensor.snapshot", "sensor.delta"}, Handles: []string{"telemetry.sensors.resync"}},
	},
	{
		Name:            ServiceHistorian,
		Description:     "Records object streams into a materialized view and answers on-demand queries.",
		RequiredFeature: FeatureHistorian,
		Capabilities:    Capabilities{Consumes: []string{"sensor.snapshot", "sensor.delta"}, Handles: []string{"historian.query"}},
	},
	{
		Name:            ServiceAlarm,
		Description:     "Evaluates alarm rules over object streams and publishes alarm state transitions.",
		RequiredFeature: FeatureAlarms,
		Capabilities:    Capabilities{Consumes: []string{"sensor.delta"}, Publishes: []string{"alarm.transition"}},
	},
	{
		Name:         ServiceIntegration,
		Description:  "Bridges platform streams to external systems (including OPC UA publication).",
		Capabilities: Capabilities{Consumes: []string{"sensor.delta", "alarm.transition"}},
	},
}

// features is the closed set of feature keys a distribution may enable.
var features = []string{
	FeatureOPCUA,
	FeatureAlarms,
	FeatureRecording,
	FeatureAnalytics,
	FeatureHistorian,
	FeatureChaos,
}

// Services returns the full catalog of hostable service kinds.
func Services() []Service {
	out := make([]Service, len(services))
	copy(out, services)
	return out
}

// LookupService returns the catalog entry for a service kind, if it exists.
func LookupService(name string) (Service, bool) {
	for _, s := range services {
		if s.Name == name {
			return s, true
		}
	}
	return Service{}, false
}

// Features returns the full set of feature keys.
func Features() []string {
	out := make([]string, len(features))
	copy(out, features)
	return out
}

// IsFeature reports whether key is a known feature key.
func IsFeature(key string) bool {
	return slices.Contains(features, key)
}
