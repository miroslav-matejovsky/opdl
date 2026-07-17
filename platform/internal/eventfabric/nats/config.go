package nats

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
	"github.com/miroslav-matejovsky/opdl/platform/internal/eventfabric"
)

const (
	// Name is this adapter's implementation name, reported on Event Fabric
	// lifecycle events.
	Name = "nats"

	// ClientPort is the fixed port the embedded server serves clients on.
	ClientPort = 4222
	// ClusterPort is the fixed port the embedded servers route to each other on.
	ClusterPort = 6222
	// MonitorPort is the fixed port the embedded server serves monitoring on. It
	// binds to loopback by default so it is not exposed off the machine.
	MonitorPort = 8222

	// smallSiteMax is the largest site that runs one JetStream storage node. A
	// site with three or more machines runs three.
	smallSiteMax = 2

	// DefaultMaxBytes bounds the journal's total size. Reaching it rejects new
	// events rather than deleting replay history.
	DefaultMaxBytes int64 = 1 << 30 // 1 GiB
	// DefaultMaxMessageBytes bounds one event's size.
	DefaultMaxMessageBytes int32 = 1 << 20 // 1 MiB

	// DefaultStartupTimeout bounds how long the embedded server may take to
	// become ready for connections.
	DefaultStartupTimeout = 30 * time.Second
	// DefaultCatchUpTimeout bounds how long a projector may take to reach the
	// high-water sequence captured at startup.
	DefaultCatchUpTimeout = 30 * time.Second
	// DefaultShutdownTimeout bounds a clean shutdown.
	DefaultShutdownTimeout = 10 * time.Second
	// DefaultAckWait bounds how long a handler may hold an unacknowledged
	// delivery before it is redelivered.
	DefaultAckWait = 30 * time.Second
	// DefaultMaxDeliver bounds how many times one event is redelivered to a
	// handler before its delivery is exhausted.
	DefaultMaxDeliver = 5
)

// Config is one embedded NATS node's settings. DefaultConfig derives a
// production one from the deployment descriptor; the fields exist so development
// and scenarios can move sockets and the data directory without rebuilding a
// machine. Nothing here is an identity: overriding an address changes where the
// node listens, never which machine it is.
type Config struct {
	// ServerName is this node's name within the site cluster. It is unique per
	// site.
	ServerName string
	// ClusterName is the site cluster's name, shared by every node of the site.
	ClusterName string

	// ClientAddress is the host:port the node serves clients on.
	ClientAddress string
	// ClusterAddress is the host:port the node routes to peers on.
	ClusterAddress string
	// MonitorAddress is the host:port the node serves monitoring on.
	MonitorAddress string
	// Routes are the cluster host:port addresses of the site's peers to route to.
	Routes []string

	// HostsStorage reports whether this node runs JetStream storage. Only the
	// storage nodes do; the rest run Core NATS and route to them.
	HostsStorage bool
	// DataDir is the JetStream file store directory. It is required on a storage
	// node and unused on a node that does not host storage.
	DataDir string
	// Replicas is the site journal's replica count.
	Replicas int
	// MaxBytes bounds the journal's total size.
	MaxBytes int64
	// MaxMessageBytes bounds one event's size.
	MaxMessageBytes int32

	// AckWait bounds an unacknowledged handler delivery before redelivery.
	AckWait time.Duration
	// MaxDeliver bounds handler redeliveries before delivery is exhausted.
	MaxDeliver int

	// StartupTimeout bounds readiness for connections.
	StartupTimeout time.Duration
	// CatchUpTimeout bounds projector catch-up to the startup high-water mark.
	CatchUpTimeout time.Duration
	// ShutdownTimeout bounds a clean shutdown.
	ShutdownTimeout time.Duration

	// Username and Password are the site credentials. They are required when any
	// address or route is non-loopback and are read from a file by the composer,
	// never from the descriptor.
	Username string
	Password string
}

// DefaultConfig derives the production configuration from a machine's resolved
// deployment descriptor: the node's own addresses from its descriptor IP, its
// routes from its peers' IPs, and its storage role and replica count from the
// site size. It leaves DataDir and credentials for the composer, which knows the
// runtime data path and reads secrets from files.
func DefaultConfig(descriptor deployment.Descriptor) (Config, error) {
	client, err := address(descriptor.IP, ClientPort)
	if err != nil {
		return Config{}, fmt.Errorf("nats: client address: %w", err)
	}
	cluster, err := address(descriptor.IP, ClusterPort)
	if err != nil {
		return Config{}, fmt.Errorf("nats: cluster address: %w", err)
	}
	monitor, err := address("127.0.0.1", MonitorPort)
	if err != nil {
		return Config{}, fmt.Errorf("nats: monitor address: %w", err)
	}

	routes := make([]string, 0, len(descriptor.Fabric.Peers))
	for _, peer := range descriptor.Fabric.Peers {
		route, err := address(peer.IP, ClusterPort)
		if err != nil {
			return Config{}, fmt.Errorf("nats: peer %q: %w", peer.Machine, err)
		}
		routes = append(routes, route)
	}

	machines := siteMachines(descriptor)
	return Config{
		ServerName:      descriptor.Machine,
		ClusterName:     string(eventfabric.NewSiteScope(descriptor.Project, descriptor.Environment, descriptor.Site)),
		ClientAddress:   client,
		ClusterAddress:  cluster,
		MonitorAddress:  monitor,
		Routes:          routes,
		HostsStorage:    slices.Contains(StorageNodes(machines), descriptor.Machine),
		Replicas:        Replicas(len(machines)),
		MaxBytes:        DefaultMaxBytes,
		MaxMessageBytes: DefaultMaxMessageBytes,
		AckWait:         DefaultAckWait,
		MaxDeliver:      DefaultMaxDeliver,
		StartupTimeout:  DefaultStartupTimeout,
		CatchUpTimeout:  DefaultCatchUpTimeout,
		ShutdownTimeout: DefaultShutdownTimeout,
	}, nil
}

// StorageNodes returns the machines that host JetStream storage for a site,
// sorted by machine name: one storage node for a site smaller than three
// machines, the first three for larger sites. Selecting a deterministic set by
// sorted name avoids forming a two-member JetStream metadata group, which would
// lose quorum when one member failed.
func StorageNodes(machines []string) []string {
	sorted := slices.Clone(machines)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	switch {
	case len(sorted) == 0:
		return nil
	case len(sorted) <= smallSiteMax:
		return sorted[:1]
	default:
		return sorted[:3]
	}
}

// Replicas returns the site journal's replica count for a site of siteSize
// machines: one replica for a site smaller than three machines, three
// otherwise. It matches the storage-node count so every replica has a host.
func Replicas(siteSize int) int {
	if siteSize <= smallSiteMax {
		return 1
	}
	return 3
}

// Validate checks the composed configuration before any listener is opened, so a
// bad address or an unwritable data directory fails at startup rather than half
// way through binding sockets or creating a stream.
func (c Config) Validate() error {
	addresses := map[string]string{
		"client address":  c.ClientAddress,
		"cluster address": c.ClusterAddress,
		"monitor address": c.MonitorAddress,
	}
	for what, addr := range addresses {
		if err := validateAddress(what, addr); err != nil {
			return err
		}
	}
	if err := uniqueAddresses(c.ClientAddress, c.ClusterAddress, c.MonitorAddress); err != nil {
		return err
	}
	for _, route := range c.Routes {
		if err := validateAddress("route", route); err != nil {
			return err
		}
		if route == c.ClusterAddress {
			return fmt.Errorf("nats: route %q is this node itself", route)
		}
	}
	if duplicate, found := firstDuplicate(c.Routes); found {
		return fmt.Errorf("nats: route %q is listed twice", duplicate)
	}
	if c.HostsStorage {
		if err := c.validateStorage(); err != nil {
			return err
		}
	}
	if c.AckWait <= 0 {
		return fmt.Errorf("nats: ack wait must be positive, got %s", c.AckWait)
	}
	if c.MaxDeliver <= 0 {
		return fmt.Errorf("nats: max deliver must be positive, got %d", c.MaxDeliver)
	}
	for what, duration := range map[string]time.Duration{
		"startup timeout":  c.StartupTimeout,
		"catch-up timeout": c.CatchUpTimeout,
		"shutdown timeout": c.ShutdownTimeout,
	} {
		if duration <= 0 {
			return fmt.Errorf("nats: %s must be positive, got %s", what, duration)
		}
	}
	return c.validateCredentials()
}

// validateStorage checks a storage node has a writable data directory and
// positive journal limits and replicas.
func (c Config) validateStorage() error {
	if strings.TrimSpace(c.DataDir) == "" {
		return fmt.Errorf("nats: data directory is required on a storage node")
	}
	if err := probeWritable(c.DataDir); err != nil {
		return err
	}
	if c.MaxBytes <= 0 {
		return fmt.Errorf("nats: max bytes must be positive, got %d", c.MaxBytes)
	}
	if c.MaxMessageBytes <= 0 {
		return fmt.Errorf("nats: max message bytes must be positive, got %d", c.MaxMessageBytes)
	}
	if c.Replicas <= 0 {
		return fmt.Errorf("nats: replicas must be positive, got %d", c.Replicas)
	}
	return nil
}

// validateCredentials requires site credentials whenever any address or route is
// non-loopback, and allows their absence only when everything is loopback, which
// is the tests-and-development case.
func (c Config) validateCredentials() error {
	all := append([]string{c.ClientAddress, c.ClusterAddress, c.MonitorAddress}, c.Routes...)
	exposed := false
	for _, addr := range all {
		loopback, err := isLoopback(addr)
		if err != nil {
			return err
		}
		if !loopback {
			exposed = true
			break
		}
	}
	if exposed && (c.Username == "" || c.Password == "") {
		return fmt.Errorf("nats: username and password are required when any address is non-loopback")
	}
	return nil
}

// siteMachines returns every machine name in the site: this machine and its
// peers.
func siteMachines(descriptor deployment.Descriptor) []string {
	machines := make([]string, 0, len(descriptor.Fabric.Peers)+1)
	machines = append(machines, descriptor.Machine)
	for _, peer := range descriptor.Fabric.Peers {
		machines = append(machines, peer.Machine)
	}
	return machines
}

// address renders a validated host:port from an IP and a port.
func address(ip string, port int) (string, error) {
	if strings.TrimSpace(ip) == "" {
		return "", fmt.Errorf("host is required")
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("port %d is out of range 1-65535", port)
	}
	return net.JoinHostPort(ip, strconv.Itoa(port)), nil
}

// validateAddress checks addr is a usable host:port.
func validateAddress(what, addr string) error {
	if strings.TrimSpace(addr) == "" {
		return fmt.Errorf("nats: %s is required", what)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("nats: %s %q must be host:port: %w", what, addr, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("nats: %s %q has no host", what, addr)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("nats: %s %q: port is not a number", what, addr)
	}
	if number < 1 || number > 65535 {
		return fmt.Errorf("nats: %s %q: port %d is out of range 1-65535", what, addr, number)
	}
	return nil
}

// uniqueAddresses reports an error when two of the node's own addresses collide.
func uniqueAddresses(addrs ...string) error {
	seen := make(map[string]bool, len(addrs))
	for _, addr := range addrs {
		if seen[addr] {
			return fmt.Errorf("nats: address %q is used more than once", addr)
		}
		seen[addr] = true
	}
	return nil
}

// firstDuplicate returns the first repeated value in values.
func firstDuplicate(values []string) (string, bool) {
	seen := make([]string, 0, len(values))
	for _, value := range values {
		if slices.Contains(seen, value) {
			return value, true
		}
		seen = append(seen, value)
	}
	return "", false
}

// isLoopback reports whether addr's host is a loopback address. A host that is
// not a bare IP is treated as non-loopback, so a named host requires
// credentials.
func isLoopback(addr string) (bool, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false, fmt.Errorf("nats: %q must be host:port: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false, nil
	}
	return ip.IsLoopback(), nil
}

// probeWritable creates dir if needed and confirms a file can be written and
// removed there, so an unusable data directory fails at startup.
func probeWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("nats: create data directory %s: %w", dir, err)
	}
	probe := filepath.Join(dir, ".write-probe")
	if err := os.WriteFile(probe, []byte("opdl nats probe"), 0o600); err != nil {
		return fmt.Errorf("nats: data directory %s is not writable: %w", dir, err)
	}
	if err := os.Remove(probe); err != nil {
		return fmt.Errorf("nats: data directory %s probe cleanup: %w", dir, err)
	}
	return nil
}
