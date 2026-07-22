package nats

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

const reconnectWait = 250 * time.Millisecond

// serverOptions translates the adapter's configuration into embedded server
// options. It enables JetStream only on a storage node and configures the
// cluster listener and routes only when the site has peers to route to. The
// server's own logs are silenced; the platform reports Event Fabric lifecycle
// through local operational events that remain available when NATS does not.
//
// No monitoring listener is configured. HTTPPort and HTTPSPort are left at zero,
// which is what makes the server start none: the runtime reads connection,
// journal high-water, projection progress, and lag through the Event Fabric
// client API and writes them to its own status files, so a second unauthenticated
// HTTP surface would add an open port without adding a signal.
func serverOptions(cfg Config) (*server.Options, error) {
	clientHost, clientPort, err := splitHostPort(cfg.ClientAddress)
	if err != nil {
		return nil, fmt.Errorf("nats: client address: %w", err)
	}

	opts := &server.Options{
		ServerName: cfg.ServerName,
		Host:       clientHost,
		Port:       clientPort,
		NoLog:      true,
		NoSigs:     true,
		JetStream:  cfg.HostsStorage,
	}
	if cfg.HostsStorage {
		opts.StoreDir = cfg.DataDir
	}
	if cfg.Username != "" {
		opts.Username = cfg.Username
		opts.Password = cfg.Password
	}
	// The cluster listener is conditional on having somewhere to route. A
	// configured cluster port is not permission to bind it: on a site whose
	// topology selects one storage node there is no peer server, so binding it
	// would open a port nothing can connect to.
	if len(cfg.Routes) > 0 {
		clusterHost, clusterPort, err := splitHostPort(cfg.ClusterAddress)
		if err != nil {
			return nil, fmt.Errorf("nats: cluster address: %w", err)
		}
		opts.Cluster = server.ClusterOpts{
			Name: cfg.ClusterName,
			Host: clusterHost,
			Port: clusterPort,
		}
		routes, err := parseRoutes(cfg.Routes)
		if err != nil {
			return nil, err
		}
		opts.Routes = routes
	}
	return opts, nil
}

// natsOptions builds the client connection options: site credentials when they
// are set, and a name that identifies this node's own client.
func natsOptions(cfg Config) []nats.Option {
	// The resolver deliberately puts a storage machine's own server first. NATS
	// randomizes URL order by default, which defeats that topology and can make a
	// healthy storage process depend on a peer it does not need. Preserve the
	// resolved order and keep reconnecting while the runtime remains within its
	// own projection-lag safety bound.
	opts := []nats.Option{
		nats.Name(cfg.ClientName),
		nats.DontRandomize(),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(reconnectWait),
	}
	if cfg.Username != "" {
		opts = append(opts, nats.UserInfo(cfg.Username, cfg.Password))
	}
	return opts
}

// parseRoutes turns cluster host:port routes into the route URLs the server
// wants.
func parseRoutes(routes []string) ([]*url.URL, error) {
	parsed := make([]*url.URL, 0, len(routes))
	for _, route := range routes {
		routeURL, err := url.Parse("nats://" + route)
		if err != nil {
			return nil, fmt.Errorf("nats: route %q: %w", route, err)
		}
		parsed = append(parsed, routeURL)
	}
	return parsed, nil
}

// splitHostPort splits a validated host:port into its host and numeric port.
func splitHostPort(addr string) (host string, port int, err error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("%q must be host:port: %w", addr, err)
	}
	port, err = strconv.Atoi(portText)
	if err != nil {
		return "", 0, fmt.Errorf("%q: port is not a number", addr)
	}
	return host, port, nil
}
