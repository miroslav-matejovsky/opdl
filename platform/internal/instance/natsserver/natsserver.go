package natsserver

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// routeScheme is the URL scheme a NATS route is written with, and the only one
// this server accepts: a peer is dialed as a route, never as a client.
const routeScheme = "nats"

// readyTimeout bounds how long Start waits for the embedded server to finish
// coming up before it gives up and shuts the server down.
//
// It is generous for a server that binds two listeners on the local host: a
// server that is not ready within it is not slow, it is not coming up, and the
// instance is better off failing at startup than running with a fabric that
// carries nothing.
//
// Reaching its peers is not part of readiness. Routes are dialed and retried in
// the background, so a member whose peers are still down comes up on time and
// joins them when they arrive.
const readyTimeout = 5 * time.Second

// loopbackHost is where the server's incidental client listener is bound. See
// Start for why one is bound at all.
const loopbackHost = "127.0.0.1"

// Config is one instance's embedded server, exactly as the deployment
// descriptor resolved it. Nothing is defaulted: a field the descriptor did not
// carry is a startup failure rather than a value guessed at here.
type Config struct {
	// Name identifies this server. It is unique within the project, so no two
	// servers that join one cluster can claim the same identity.
	Name string
	// ClusterName is the cluster this server belongs to. Servers only route to
	// peers that name the same cluster, so it is what bounds the fabric.
	ClusterName string
	// ClusterAddress is the host:port this server accepts route connections
	// from its peers on. It is on the machine's own ip rather than on loopback,
	// because the peers are on other machines.
	ClusterAddress string
	// Routes are the peers this server dials to join the cluster, as NATS route
	// URLs ("nats://10.0.1.11:6222").
	//
	// Empty is a valid cluster of one: a site that deploys a single instance has
	// no peer. A peer that is not up yet is not an error either; the server
	// keeps trying and the cluster forms whenever the peer arrives.
	Routes []string
}

// Server is the embedded NATS server one platform instance runs.
type Server struct {
	config   Config
	instance *server.Server
}

// Start validates cfg, starts the embedded server, and returns once it has
// finished coming up.
//
// The server accepts no client connections over the network: the only client is
// the platform's own, in this process, and it is handed a connection by
// InProcessConn rather than dialing anything. What is bound on the machine's ip
// is the route listener, and what is dialed are the site's other members. The
// two together are how this instance joins its site's cluster.
//
// A server that does not come up within readyTimeout is shut down before the
// error is returned, so a failed start leaves no listener bound and no
// goroutine running.
func Start(cfg Config) (*Server, error) {
	host, port, err := clusterHostPort(cfg.ClusterAddress)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("embedded NATS server: name is required")
	}
	if strings.TrimSpace(cfg.ClusterName) == "" {
		return nil, fmt.Errorf("embedded NATS server %q: cluster name is required", cfg.Name)
	}
	routes, err := routeURLs(cfg)
	if err != nil {
		return nil, err
	}

	instance, err := server.NewServer(&server.Options{
		ServerName: cfg.Name,
		// The client listener is on loopback and on whatever port the kernel
		// hands out. Nothing dials it: the platform's only client connects in
		// process, and no client outside this process exists.
		//
		// It is bound at all because the server's own startup requires it. NATS
		// starts routing only once the client listener is up, so a server told
		// not to listen never binds its route listener either and never becomes
		// ready. An ephemeral loopback port is what satisfies that without
		// reserving a port a deployment would have to author, and without
		// offering a way in from the network.
		Host: loopbackHost,
		Port: server.RANDOM_PORT,
		// The route listener is what this server binds on purpose, on the
		// machine's own ip so the site's other members can reach it.
		Cluster: server.ClusterOpts{
			Name: cfg.ClusterName,
			Host: host,
			Port: port,
		},
		// The peers this server dials. Routes are mutual, so every member
		// carries every other member and the cluster forms regardless of which
		// one started first. A peer that is not listening yet is retried in the
		// background; it does not hold up this server coming up.
		Routes: routes,
		// The platform owns the process's signal handling. A server that
		// installed its own would take the graceful stop away from the
		// composition root.
		NoSigs: true,
		// The server writes nothing on its own. What it has to say goes through
		// the logger installed below, into the instance's application log,
		// rather than onto a stream nobody is reading.
		NoLog: true,
		// No JetStream: this transport carries messages between connected peers
		// and stores nothing. See the package documentation.
		JetStream: false,
	})
	if err != nil {
		return nil, fmt.Errorf("create embedded NATS server %q: %w", cfg.Name, err)
	}
	// Installed before Start, so a listener the server cannot bind is described
	// in the instance's log rather than swallowed. NoLog above keeps the server
	// from also configuring one of its own.
	instance.SetLogger(slogLogger{log: slog.Default().With("component", "nats", "nats_server_name", cfg.Name)}, false, false)

	instance.Start()
	if !instance.ReadyForConnections(readyTimeout) {
		instance.Shutdown()
		instance.WaitForShutdown()
		return nil, fmt.Errorf("embedded NATS server %q was not ready within %s; its route listener on %s is the usual reason",
			cfg.Name, readyTimeout, cfg.ClusterAddress)
	}
	return &Server{config: cfg, instance: instance}, nil
}

// InProcessConn returns a connection to this server that never leaves the
// process.
//
// It is the whole of what a client needs from the server, which is why it is the
// only method a client is given: the site level names this one capability and
// takes it from whatever provides it, rather than depending on the instance
// level's package.
func (s *Server) InProcessConn() (net.Conn, error) {
	conn, err := s.instance.InProcessConn()
	if err != nil {
		return nil, fmt.Errorf("open an in-process connection to embedded NATS server %q: %w", s.config.Name, err)
	}
	return conn, nil
}

// Close shuts the server down and waits until it has stopped.
//
// It returns nothing because nothing about it can fail: shutting a running
// server down is not an operation that reports an outcome, and waiting for it is
// what makes the process's exit orderly rather than a race with a listener that
// is still bound.
func (s *Server) Close() {
	s.instance.Shutdown()
	s.instance.WaitForShutdown()
}

// routeURLs parses the configured peers into what the server options need.
//
// The builder resolved them and the descriptor was validated at load, and they
// are parsed again here for the same reason the cluster address is: a
// hand-edited descriptor must fail before the server starts rather than leave
// one member silently outside its site's cluster.
func routeURLs(cfg Config) ([]*url.URL, error) {
	if len(cfg.Routes) == 0 {
		return nil, nil
	}
	routes := make([]*url.URL, 0, len(cfg.Routes))
	for _, route := range cfg.Routes {
		parsed, err := url.Parse(strings.TrimSpace(route))
		if err != nil {
			return nil, fmt.Errorf("embedded NATS server %q: route %q is not a URL: %w", cfg.Name, route, err)
		}
		if parsed.Scheme != routeScheme || parsed.Host == "" {
			return nil, fmt.Errorf("embedded NATS server %q: route %q must be %s://host:port", cfg.Name, route, routeScheme)
		}
		routes = append(routes, parsed)
	}
	return routes, nil
}

// clusterHostPort splits a resolved cluster address into what the server
// options need.
//
// The builder already validated it, and it is checked again here for the same
// reason the descriptor's durations are parsed at startup: a hand-edited or
// truncated descriptor must fail before a broker binds something unintended,
// not after.
func clusterHostPort(address string) (host string, port int, err error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return "", 0, fmt.Errorf("embedded NATS server: cluster address %q must be host:port: %w", address, err)
	}
	if net.ParseIP(host) == nil {
		return "", 0, fmt.Errorf("embedded NATS server: cluster address %q has no usable host", address)
	}
	port, err = strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("embedded NATS server: cluster address %q has no usable port", address)
	}
	return host, port, nil
}
