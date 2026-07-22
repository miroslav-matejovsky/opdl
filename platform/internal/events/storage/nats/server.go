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
		opts.StoreDir = cfg.JetStreamStoreDir
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

func natsOptions(cfg Config) []nats.Option {
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
