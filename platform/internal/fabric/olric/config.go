package olric

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

const (
	// Name is this adapter's implementation name, reported as operational
	// metadata on fabric events.
	Name = "olric"

	// DefaultStartTimeout bounds readiness. It is generous on purpose: a member
	// whose peers are not up yet pays a memberlist join timeout (about ten
	// seconds) before starting alone, and a site that boots in any order must
	// not fail because it booted first.
	DefaultStartTimeout = 30 * time.Second
)

// Config is one embedded member's settings. DefaultConfig derives a production
// one from the deployment descriptor; the fields exist so development and
// scenarios can move sockets without rebuilding a machine.
//
// Nothing here is an identity. Overriding an address changes where the member
// listens, never which machine it is: that comes from the descriptor alone.
type Config struct {
	// ClientAddress is the host:port of the member's client surface.
	ClientAddress string
	// MemberlistAddress is the host:port the member gossips membership on.
	MemberlistAddress string
	// Join are the memberlist host:port addresses of the peers to seed from.
	// Empty means this member starts a fabric rather than joining one.
	Join []string
	// StartTimeout bounds how long the member may take to become ready.
	StartTimeout time.Duration
	// ShutdownGrace bounds how long the member may take to tear down cleanly.
	ShutdownGrace time.Duration
}

// DefaultConfig reads the selfInstance process's explicit production endpoints
// from the resolved deployment descriptor. It seeds from every other expected
// instance of the site, including this machine's sibling instance, so a primary
// and secondary on one machine find each other and both find every peer
// instance. Only the selected local instance is excluded. No endpoint or port is
// invented here.
func DefaultConfig(descriptor deployment.Descriptor, selfInstance string) (Config, error) {
	self, found := instanceByName(descriptor.PlatformInstances, selfInstance)
	if !found {
		return Config{}, fmt.Errorf("olric: platform instance %q is absent from deployment descriptor", selfInstance)
	}
	join := make([]string, 0, len(descriptor.PlatformInstances)+len(descriptor.Fabric.Peers))
	// The sibling instance on this machine, when there is one. Its endpoints are
	// as explicit as any peer's, and a member never seeds from itself.
	for _, instance := range descriptor.PlatformInstances {
		if instance.Name == selfInstance {
			continue
		}
		join = append(join, instance.FabricMemberlistAddress)
	}
	// Every peer instance of the site, both primary and secondary.
	for _, peer := range descriptor.Fabric.Peers {
		join = append(join, peer.FabricMemberlistAddress)
	}
	config := Config{
		ClientAddress:     self.FabricClientAddress,
		MemberlistAddress: self.FabricMemberlistAddress,
		Join:              join,
		StartTimeout:      DefaultStartTimeout,
		ShutdownGrace:     10 * time.Second,
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("olric: deployment endpoints: %w", err)
	}
	return config, nil
}

// instanceByName returns the named platform instance from a descriptor's list.
func instanceByName(instances []deployment.PlatformInstance, name string) (deployment.PlatformInstance, bool) {
	for _, instance := range instances {
		if instance.Name == name {
			return instance, true
		}
	}
	return deployment.PlatformInstance{}, false
}

// Validate checks the composed configuration before any listener is opened, so
// a bad address fails at startup rather than half way through binding sockets.
func (c Config) Validate() error {
	if err := validateAddress("client address", c.ClientAddress); err != nil {
		return err
	}
	if err := validateAddress("memberlist address", c.MemberlistAddress); err != nil {
		return err
	}
	if c.ClientAddress == c.MemberlistAddress {
		return fmt.Errorf("olric: client and memberlist addresses are both %q", c.ClientAddress)
	}
	for _, join := range c.Join {
		if err := validateAddress("join address", join); err != nil {
			return err
		}
		if join == c.MemberlistAddress {
			return fmt.Errorf("olric: join address %q is this member itself", join)
		}
	}
	if duplicate, found := firstDuplicate(c.Join); found {
		return fmt.Errorf("olric: join address %q is listed twice", duplicate)
	}
	if c.StartTimeout <= 0 {
		return fmt.Errorf("olric: start timeout must be positive, got %s", c.StartTimeout)
	}
	if c.ShutdownGrace <= 0 {
		return fmt.Errorf("olric: shutdown grace must be positive, got %s", c.ShutdownGrace)
	}
	return nil
}

// validateAddress checks addr is a host:port a member can use.
func validateAddress(what, addr string) error {
	if strings.TrimSpace(addr) == "" {
		return fmt.Errorf("olric: %s is required", what)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("olric: %s %q must be host:port: %w", what, addr, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("olric: %s %q has no host", what, addr)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("olric: %s %q: port is not a number", what, addr)
	}
	if number < 1 || number > 65535 {
		return fmt.Errorf("olric: %s %q: port %d is out of range 1-65535", what, addr, number)
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

// splitAddress splits a validated host:port for Olric, which wants them apart.
func splitAddress(addr string) (host string, port int, err error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("olric: %q must be host:port: %w", addr, err)
	}
	number, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, errors.New("olric: port is not a number")
	}
	return host, number, nil
}
