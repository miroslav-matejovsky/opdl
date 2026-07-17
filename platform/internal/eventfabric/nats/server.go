package nats

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// serverOptions translates the adapter's configuration into embedded server
// options. It enables JetStream only on a storage node, configures the cluster
// listener and routes only when the site has peers, and binds monitoring where
// configured. The server's own logs are silenced; the platform reports Event
// Fabric lifecycle through its own journal instead.
func serverOptions(cfg Config) (*server.Options, error) {
	clientHost, clientPort, err := splitHostPort(cfg.ClientAddress)
	if err != nil {
		return nil, fmt.Errorf("nats: client address: %w", err)
	}
	monitorHost, monitorPort, err := splitHostPort(cfg.MonitorAddress)
	if err != nil {
		return nil, fmt.Errorf("nats: monitor address: %w", err)
	}

	opts := &server.Options{
		ServerName: cfg.ServerName,
		Host:       clientHost,
		Port:       clientPort,
		HTTPHost:   monitorHost,
		HTTPPort:   monitorPort,
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
	opts := []nats.Option{nats.Name(cfg.ServerName)}
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
