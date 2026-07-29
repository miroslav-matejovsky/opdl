package healthfabric

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/miroslav-matejovsky/opdl/platform/internal/site/healthview"
)

// Subject is the one subject site health travels on.
//
// It is fixed rather than composed from anything authored, so no deployment can
// produce a subject another one is listening to by accident, and no name from a
// blueprint reaches a broker as a wildcard. The version is in the subject
// rather than only in the payload: a future contract that cannot be read by
// today's receivers gets its own subject and is simply not delivered to them,
// which is a cleaner failure than a decode error on every message.
const Subject = "opdl.service_health.v1"

// Version is the payload version carried inside a message on Subject.
//
// It is checked as well as the subject, because a sender built from a newer
// contract could publish to this subject and a receiver that decoded it
// leniently would apply fields it does not understand.
const Version = 1

// MaxMessageBytes bounds a message a receiver will decode.
//
// A health observation is a few hundred bytes and the only field that varies is
// the probe error, which is a service's own text. This is what stops one
// misbehaving machine turning every receiver's decode into unbounded work.
const MaxMessageBytes = 8 << 10

// maxErrorBytes bounds the probe error a sender puts on the wire. It is
// truncated rather than dropped: the first part of a failure is what identifies
// it, and a receiver that saw nothing would show a failing service with no
// reason at all.
const maxErrorBytes = 512

// maxLatency is the longest attempt duration a receiver believes.
//
// An attempt is bounded by its own authored timeout, which is shorter than its
// interval, so any real latency is small. A message claiming hours is a sender
// with a broken clock or a corrupted payload, and applying it would put a
// nonsense number in front of an operator.
const maxLatency = time.Hour

// message is the JSON one observation travels as.
//
// The field names are explicit and stable: this is a contract between separately
// built binaries at one site, and a machine on an older build has to be able to
// read what a newer one sends for as long as the version matches.
type message struct {
	Version     int    `json:"v"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Site        string `json:"site"`
	Machine     string `json:"machine"`
	Service     string `json:"service"`
	// ObserverRole is which of the hosting machine's instances reported.
	ObserverRole string `json:"observer_role"`
	// Epoch is the sender's process-start count and Sequence orders messages
	// within it.
	Epoch    uint64 `json:"epoch"`
	Sequence uint64 `json:"sequence"`
	Status   string `json:"status"`
	// CheckedAtUTC is the sender's own clock, carried for a reader and used by
	// no decision on the receiving side.
	CheckedAtUTC        time.Time `json:"checked_at_utc"`
	LatencyMS           int64     `json:"latency_ms"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	Error               string    `json:"error,omitempty"`
}

// Identity is who a publisher says it is.
//
// It is fixed for the life of the process: the deployment it belongs to, the
// machine it runs on, which of that machine's instances it is, and the epoch
// that tells this incarnation from the last. A publisher that could change any
// of it at runtime would be one whose messages a receiver could not fence.
type Identity struct {
	Deployment   healthview.Deployment
	Machine      string
	ObserverRole string
	Epoch        uint64
}

func (i Identity) validate() error {
	if err := i.Deployment.Validate(); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(i.Machine) == "":
		return fmt.Errorf("machine is required")
	case strings.TrimSpace(i.ObserverRole) == "":
		return fmt.Errorf("observer role is required")
	case i.Epoch == 0:
		return fmt.Errorf("epoch is required; it is the process-start count that fences a restart")
	}
	return nil
}

// encode renders one local observation as the bytes that go on the wire.
func encode(identity Identity, observation healthview.Observation) ([]byte, error) {
	failure := observation.Error
	if len(failure) > maxErrorBytes {
		failure = failure[:maxErrorBytes]
	}
	return json.Marshal(message{
		Version:             Version,
		Project:             identity.Deployment.Project,
		Environment:         identity.Deployment.Environment,
		Site:                identity.Deployment.Site,
		Machine:             observation.Unit.Machine,
		Service:             observation.Unit.Service,
		ObserverRole:        observation.ObserverRole,
		Epoch:               observation.Epoch,
		Sequence:            observation.Sequence,
		Status:              string(observation.Status),
		CheckedAtUTC:        observation.CheckedAtUTC.UTC(),
		LatencyMS:           observation.Latency.Milliseconds(),
		ConsecutiveFailures: observation.ConsecutiveFailures,
		Error:               failure,
	})
}

// RejectReason says why a delivered message was not turned into an observation.
//
// These are the faults a receiver can see in the bytes alone. Whether the
// observation is about something this site contains, and whether it is newer
// than what is already held, are the view's questions and are answered there.
type RejectReason string

const (
	// RejectOversize is a payload past MaxMessageBytes. It is measured before
	// decoding, so an enormous message costs a length check rather than a parse.
	RejectOversize RejectReason = "oversize"
	// RejectMalformed is a payload that is not the JSON this contract describes.
	RejectMalformed RejectReason = "malformed"
	// RejectVersion is a payload built to a different version of this contract.
	RejectVersion RejectReason = "version"
	// RejectForeign is a message about a different deployment. Two deployments
	// sharing a broker is a misconfiguration, and it should be visible as one
	// rather than silently mixing two sites' services.
	RejectForeign RejectReason = "foreign_deployment"
	// RejectIncomplete is a message missing an identity field, so it names no
	// unit or no observer.
	RejectIncomplete RejectReason = "incomplete"
	// RejectImpossible is a message whose numbers cannot describe a real attempt:
	// a negative or absurd latency, a negative failure count, or a zero epoch.
	RejectImpossible RejectReason = "impossible"
)

// RejectReasons is every reason a receiver counts, in a fixed order so two
// processes render their counters the same way.
var RejectReasons = []RejectReason{
	RejectOversize,
	RejectMalformed,
	RejectVersion,
	RejectForeign,
	RejectIncomplete,
	RejectImpossible,
}

// decode turns delivered bytes into an observation, or says why it will not.
//
// Everything checkable without the inventory is checked here, so what reaches
// the view is a well-formed report about this deployment and the view is left
// to answer only what it alone knows: whether the unit exists and whether the
// report is newer than what it holds.
func decode(deployment healthview.Deployment, data []byte) (healthview.Observation, RejectReason, error) {
	if len(data) > MaxMessageBytes {
		return healthview.Observation{}, RejectOversize,
			fmt.Errorf("message is %d bytes, past the %d-byte bound", len(data), MaxMessageBytes)
	}
	var decoded message
	if err := json.Unmarshal(data, &decoded); err != nil {
		return healthview.Observation{}, RejectMalformed, fmt.Errorf("decode: %w", err)
	}
	if decoded.Version != Version {
		return healthview.Observation{}, RejectVersion,
			fmt.Errorf("payload version %d is not %d", decoded.Version, Version)
	}
	if decoded.Project != deployment.Project || decoded.Environment != deployment.Environment || decoded.Site != deployment.Site {
		return healthview.Observation{}, RejectForeign,
			fmt.Errorf("message is about %s/%s/%s, not %s/%s/%s",
				decoded.Project, decoded.Environment, decoded.Site,
				deployment.Project, deployment.Environment, deployment.Site)
	}
	switch {
	case strings.TrimSpace(decoded.Machine) == "":
		return healthview.Observation{}, RejectIncomplete, fmt.Errorf("message names no machine")
	case strings.TrimSpace(decoded.Service) == "":
		return healthview.Observation{}, RejectIncomplete, fmt.Errorf("message names no service")
	case strings.TrimSpace(decoded.ObserverRole) == "":
		return healthview.Observation{}, RejectIncomplete, fmt.Errorf("message names no observer role")
	case strings.TrimSpace(decoded.Status) == "":
		return healthview.Observation{}, RejectIncomplete, fmt.Errorf("message states no status")
	}
	latency := time.Duration(decoded.LatencyMS) * time.Millisecond
	switch {
	case decoded.Epoch == 0:
		return healthview.Observation{}, RejectImpossible, fmt.Errorf("message carries no epoch")
	case latency < 0 || latency > maxLatency:
		return healthview.Observation{}, RejectImpossible,
			fmt.Errorf("latency %dms could not have been an attempt", decoded.LatencyMS)
	case decoded.ConsecutiveFailures < 0:
		return healthview.Observation{}, RejectImpossible,
			fmt.Errorf("consecutive failures %d is negative", decoded.ConsecutiveFailures)
	}
	return healthview.Observation{
		Unit:                healthview.UnitKey{Machine: decoded.Machine, Service: decoded.Service},
		ObserverRole:        decoded.ObserverRole,
		Epoch:               decoded.Epoch,
		Sequence:            decoded.Sequence,
		Status:              healthview.Status(decoded.Status),
		CheckedAtUTC:        decoded.CheckedAtUTC,
		Latency:             latency,
		ConsecutiveFailures: decoded.ConsecutiveFailures,
		Error:               decoded.Error,
	}, "", nil
}
