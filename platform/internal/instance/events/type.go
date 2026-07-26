package events

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalidEventType reports an event type that is not in
// platform.<source>.<fact> form. Every event type is a compiled-in constant, so
// a malformed one is a programming error caught before anything is stamped.
var ErrInvalidEventType = errors.New("events: invalid event type")

// TypePrefix is the fixed first token of every event type. It names the product
// line, so an OPDL fact stays recognizable in a stream that carries more than
// this platform's events.
const TypePrefix = "platform"

// Type is the stable, dotted identifier of an event kind, e.g.
// "platform.registration.accepted". It is the discriminator stored on every
// envelope and the key readers filter on.
//
// A type has exactly three tokens, platform.<source>.<fact>:
//
//   - platform is the fixed prefix;
//   - source names the subsystem that owns the fact, e.g. "registration";
//   - fact names what happened, in the past tense, e.g. "accepted".
//
// The type is the single declaration of both the kind and its source. An
// envelope's Source is derived from the middle token, so an event never
// restates it and the two can never disagree.
type Type string

// typeToken matches one usable source or fact token: lower-case words joined by
// single underscores, e.g. "registration" or "event_fabric". Digits, casing,
// and leading, trailing, or doubled underscores are rejected so one fact has
// exactly one spelling for its whole life.
var typeToken = regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)

// Validate reports whether t is a well-formed event type. It cannot check that
// the fact reads as something completed rather than as a command; that is a
// review rule stated in the package documentation.
func (t Type) Validate() error {
	tokens := strings.Split(string(t), ".")
	if len(tokens) != 3 {
		return fmt.Errorf("%w: %q must have exactly three tokens platform.<source>.<fact>", ErrInvalidEventType, t)
	}
	if tokens[0] != TypePrefix {
		return fmt.Errorf("%w: %q must start with %q", ErrInvalidEventType, t, TypePrefix)
	}
	for i, part := range []string{"source", "fact"} {
		if !typeToken.MatchString(tokens[i+1]) {
			return fmt.Errorf("%w: %q has an unusable %s token %q, expected lower-case words joined by underscores",
				ErrInvalidEventType, t, part, tokens[i+1])
		}
	}
	return nil
}

// Source returns the subsystem token a well-formed type names, and an empty
// string for a type that is not well formed. Callers that stamp an envelope
// validate the type first, so an empty source only ever reaches a caller that
// skipped that check.
func (t Type) Source() string {
	tokens := strings.Split(string(t), ".")
	if len(tokens) != 3 {
		return ""
	}
	return tokens[1]
}
