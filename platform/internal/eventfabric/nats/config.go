package nats

import (
	"fmt"
	"maps"
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

// Config is one node's Event Fabric transport settings. DefaultConfig derives a
// production one from the deployment descriptor; the fields exist so development
// and scenarios can move sockets and the data directory without rebuilding a
// machine. Nothing here is an identity: overriding an address changes where the
// node listens or connects, never which machine it is.
type Config struct {
	// ClientName identifies this machine's connection in NATS diagnostics. It is
	// always set, including on a machine that does not host a server.
	ClientName string
	// ServerName is this node's name within the site cluster. It is unique per
	// site. It is only meaningful on a storage node.
	ServerName string
	// ClusterName is the site cluster's name, shared by the site's storage nodes.
	ClusterName string

	// ClientAddress is the host:port this node's server serves clients on. It is
	// only used on a storage node.
	ClientAddress string
	// ClusterAddress is the host:port this node's server routes to peers on. It
	// is only used on a storage node, and only when Routes is non-empty: a site
	// with one storage node has no peer to route to and binds no cluster
	// listener.
	ClusterAddress string
	// Routes are the cluster host:port addresses of the site's other storage
	// nodes. Only storage nodes route to each other, and a site with one storage
	// node has none.
	Routes []string

	// Servers are the client host:port addresses this node's Event Fabric client
	// connects to. A storage node lists its own server first and then the other
	// storage nodes, so its client-only standby can remain connected across local
	// active loss when the site has another storage node.
	Servers []string

	// HostsStorage reports whether this node runs the site journal. Only the
	// storage nodes run a NATS server at all; every other machine of the site is
	// a client of theirs.
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
// deployment descriptor: which machines store the site journal, and therefore
// whether this one runs a server at all, which peers it clusters with, and which
// servers it connects to. It leaves DataDir and credentials for the composer,
// which knows the runtime data path and reads secrets from files.
//
// It takes no instance role, and reads the Primary Instance's topology whichever
// instance is running.
//
// That is deliberately behind the descriptor contract. The descriptor now
// resolves a NATS topology per instance, because each instance is meant to run
// its own server and cluster with the other. The runtime does not do that yet: a
// standby is still turned client-only by the composer and follows the journal on
// the address the Active instance is serving, which is the primary's. Reading the
// standby's own topology here would point it at an address nothing is listening
// on, which is the failure this shape was written to prevent.
//
// Take the per-instance topology only together with the runtime change that
// starts the standby's server.
func DefaultConfig(descriptor deployment.Descriptor) (Config, error) {
	ips, err := siteIPs(descriptor)
	if err != nil {
		return Config{}, err
	}
	storage := StorageNodes(slices.Sorted(maps.Keys(ips)))
	hostsStorage := slices.Contains(storage, descriptor.Machine)

	nats := descriptor.Instances.Primary.Nats
	if nats == nil {
		return Config{}, fmt.Errorf("nats: descriptor has no primary instance topology")
	}

	cfg := Config{
		ClientName:      descriptor.Machine,
		ClusterName:     string(eventfabric.NewSiteScope(descriptor.Project, descriptor.Environment, descriptor.Site)),
		Servers:         append([]string(nil), nats.Servers...),
		Routes:          append([]string(nil), nats.Routes...),
		HostsStorage:    hostsStorage,
		Replicas:        Replicas(len(ips)),
		MaxBytes:        DefaultMaxBytes,
		MaxMessageBytes: DefaultMaxMessageBytes,
		AckWait:         DefaultAckWait,
		MaxDeliver:      DefaultMaxDeliver,
		StartupTimeout:  DefaultStartupTimeout,
		CatchUpTimeout:  DefaultCatchUpTimeout,
		ShutdownTimeout: DefaultShutdownTimeout,
	}
	if hostsStorage {
		cfg.ServerName = descriptor.Machine
		cfg.ClientAddress = nats.ClientAddress
		cfg.ClusterAddress = nats.ClusterAddress
	}
	return cfg, nil
}

// siteIPs maps every machine of the site to its address.
//
// Peers are instances, and a machine that deploys both contributes two of them,
// so the map collapses them back to machines. Storage selection and the replica
// count are both per machine: a machine is the failure domain, and two copies of
// the journal on one host is one copy as far as losing that host is concerned.
func siteIPs(descriptor deployment.Descriptor) (map[string]string, error) {
	if strings.TrimSpace(descriptor.Machine) == "" {
		return nil, fmt.Errorf("nats: descriptor has no machine")
	}
	ips := map[string]string{descriptor.Machine: descriptor.IP}
	for _, peer := range descriptor.Peers {
		if strings.TrimSpace(peer.Machine) == "" {
			return nil, fmt.Errorf("nats: peer has no machine")
		}
		ips[peer.Machine] = peer.IP
	}
	return ips, nil
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
	if strings.TrimSpace(c.ClientName) == "" {
		return fmt.Errorf("nats: client name is required")
	}
	if len(c.Servers) == 0 {
		return fmt.Errorf("nats: no server to connect to: the site has no storage node")
	}
	for _, addr := range c.Servers {
		if err := validateAddress("server", addr); err != nil {
			return err
		}
	}
	if duplicate, found := firstDuplicate(c.Servers); found {
		return fmt.Errorf("nats: server %q is listed twice", duplicate)
	}
	// Only a storage node runs a server, so only a storage node has listeners to
	// check or peers to route to. A machine that does not store the journal is a
	// client of the ones that do, and has nothing to bind.
	if c.HostsStorage {
		if err := c.validateServer(); err != nil {
			return err
		}
	} else if len(c.Routes) > 0 {
		return fmt.Errorf("nats: a node that does not store the journal has no cluster to route to")
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

// validateServer checks a storage node's listeners, its routes to the site's
// other storage nodes, and the storage its journal needs.
func (c Config) validateServer() error {
	if strings.TrimSpace(c.ServerName) == "" {
		return fmt.Errorf("nats: server name is required on a storage node")
	}
	if strings.TrimSpace(c.ClusterName) == "" {
		return fmt.Errorf("nats: cluster name is required on a storage node")
	}
	for what, addr := range map[string]string{
		"client address":  c.ClientAddress,
		"cluster address": c.ClusterAddress,
	} {
		if err := validateAddress(what, addr); err != nil {
			return err
		}
	}
	if err := uniqueAddresses(c.ClientAddress, c.ClusterAddress); err != nil {
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
	if !slices.Contains(c.Servers, c.ClientAddress) {
		return fmt.Errorf("nats: a storage node must connect to its own server %q", c.ClientAddress)
	}
	return c.validateStorage()
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

// validateCredentials requires site credentials whenever any address this node
// binds or reaches is non-loopback, and allows their absence only when
// everything is loopback, which is the tests-and-development case.
func (c Config) validateCredentials() error {
	all := slices.Concat(c.Servers, c.Routes)
	if c.HostsStorage {
		all = append(all, c.ClientAddress, c.ClusterAddress)
	}
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
