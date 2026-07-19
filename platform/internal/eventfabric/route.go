package eventfabric

import (
	"encoding/base32"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/miroslav-matejovsky/opdl/platform/internal/events"
	"github.com/miroslav-matejovsky/opdl/utils/stablehash"
)

// ErrInvalidEventType reports an event type that is not in platform.<domain>.<fact>
// form. Domain packages pass event types, never raw subjects, so a malformed
// type is a programming error caught before anything is published.
var ErrInvalidEventType = errors.New("eventfabric: invalid event type")

// eventTypePrefix is the fixed first token of every OPDL event type. It names
// the product line and is dropped from the route, which already scopes by site.
const eventTypePrefix = "platform"

// routePrefix is the fixed first token of every OPDL route. It namespaces every
// OPDL subject under one root so a shared transport keeps OPDL traffic apart
// from anything else.
const routePrefix = "opdl"

// routeInfix separates the site scope from the domain and fact in a route. It
// leaves room to add other OPDL destination kinds beside "event" later without
// colliding with the event routes.
const routeInfix = "event"

// EventType is a parsed platform.<domain>.<fact> event type: the domain that
// owns the fact and the fact itself. It is the only form the Event Fabric turns
// into a route.
type EventType struct {
	// Domain is the owning domain token, e.g. "registration".
	Domain string
	// Fact is the past-tense fact token, e.g. "accepted".
	Fact string
}

// ParseEventType splits a platform.<domain>.<fact> event type into its domain
// and fact. It rejects a type that does not start with the platform prefix, that
// has any blank token, or that carries more or fewer than three tokens, so a
// route is only ever built from a well-formed type.
func ParseEventType(eventType events.Type) (EventType, error) {
	tokens := strings.Split(string(eventType), ".")
	if len(tokens) != 3 {
		return EventType{}, fmt.Errorf("%w: %q must have exactly three tokens platform.<domain>.<fact>", ErrInvalidEventType, eventType)
	}
	if tokens[0] != eventTypePrefix {
		return EventType{}, fmt.Errorf("%w: %q must start with %q", ErrInvalidEventType, eventType, eventTypePrefix)
	}
	if slices.Contains(tokens, "") {
		return EventType{}, fmt.Errorf("%w: %q has a blank token", ErrInvalidEventType, eventType)
	}
	return EventType{Domain: tokens[1], Fact: tokens[2]}, nil
}

// SiteScope is a stable, transport-safe token that isolates one site's journal
// and routes from another's. It is derived from deployment identity but is not
// meant to be read: the human-readable project, environment, and site stay in
// the event envelope. Two sites with otherwise identical events never collide,
// and the same site always derives the same scope.
type SiteScope string

// NewSiteScope derives the scope for one site from its project, environment, and
// site. It hashes the three values length-prefixed, so no two different triples
// can encode the same bytes, and renders the hash as lower-case unpadded base32,
// which is safe in both a subject token and a stream name.
func NewSiteScope(project, environment, site string) SiteScope {
	return SiteScope(SafeToken(project, environment, site))
}

// SafeToken renders values as one stable, transport-safe token: a lower-case
// unpadded base32 encoding of a SHA-256 over the length-prefixed values. The
// same values always produce the same token, and no two different value lists
// collide. Routes, journal names, and durable consumer names are all built from
// safe tokens, so deployment identity never leaks a character a subject or a
// stream name cannot hold.
func SafeToken(values ...string) string {
	sum := stablehash.Sum256(values...)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return strings.ToLower(encoded)
}

// StreamName is the site journal's name: OPDL_<UPPER_SITE_SCOPE>_EVENTS. It is
// upper case because a stream name is not a subject token and reads more clearly
// in that form, and the base32 scope has a stable upper-case rendering.
func (s SiteScope) StreamName() string {
	return "OPDL_" + strings.ToUpper(string(s)) + "_EVENTS"
}

// SubjectFilter is the single subject the site journal binds:
// opdl.<site-scope>.event.>. It captures every event route for the site and
// nothing from any other site.
func (s SiteScope) SubjectFilter() string {
	return routePrefix + "." + string(s) + "." + routeInfix + ".>"
}

// Route is the resolved OPDL destination of one event type at one site. It is
// derived, never supplied: a domain passes an event and the Fabric builds the
// route, so a domain never constructs a subject itself.
type Route struct {
	scope SiteScope
	event EventType
}

// NewRoute builds the route for eventType at scope. It fails when eventType is
// not a well-formed platform.<domain>.<fact> type.
func NewRoute(scope SiteScope, eventType events.Type) (Route, error) {
	parsed, err := ParseEventType(eventType)
	if err != nil {
		return Route{}, err
	}
	return Route{scope: scope, event: parsed}, nil
}

// Subject is the route's subject: opdl.<site-scope>.event.<domain>.<fact>. It is
// the destination an event is published to and the leaf a handler filters on.
func (r Route) Subject() string {
	return strings.Join([]string{routePrefix, string(r.scope), routeInfix, r.event.Domain, r.event.Fact}, ".")
}


