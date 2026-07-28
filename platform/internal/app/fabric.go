package app

import (
	"github.com/miroslav-matejovsky/opdl/platform/config"
	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/natsserver"
	"github.com/miroslav-matejovsky/opdl/platform/internal/machine/redundancy"
)

// This file adapts the descriptor's embedded event fabric record into what the
// instance's broker and the site's client need, so neither package reads a
// descriptor of its own.

// fabricServerConfig builds the embedded broker's configuration from the
// running instance's own descriptor record. Every field is required in a
// descriptor, so there is nothing to default and nothing to decide here.
func fabricServerConfig(instance config.Instance) natsserver.Config {
	return natsserver.Config{
		Name:           instance.NATS.ServerName,
		ClusterName:    instance.NATS.ClusterName,
		ClusterAddress: instance.NATS.ClusterAddress,
		Routes:         instance.NATS.Routes,
	}
}

// fabricClientName names this instance's connection to its own broker.
//
// It is what an operator sees when listing the broker's connections. The
// machine and the role are both in it because a host runs two brokers with two
// connections, and a name that did not say which instance held one would leave
// an operator with two identical entries.
func fabricClientName(descriptor config.Descriptor, role redundancy.InstanceRole) string {
	return "opdl-platform-" + descriptor.Machine + "-" + role.String()
}
