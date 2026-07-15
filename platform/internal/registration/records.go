package registration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// This file owns registration's storage vocabulary: the collections it keeps its
// state in, the keys it stores records under, the records themselves, and the
// fingerprint that scopes a confirmation to the data it was made on.
//
// All of it is internal. The fabric carries opaque bytes and never interprets
// them, so the encoding is registration's alone to choose and to change; nothing
// here appears in the public API, and no other package may depend on it.

const (
	// collectionRequests holds one proposal per registration key: what was
	// asked for, where it was asked, and the fingerprint of that exact
	// proposal. A record here is a request, not a registration. It is never
	// removed or modified, so the key it occupies is the site's create-if-absent
	// guard for both pending and accepted registrations.
	collectionRequests = "registration-requests"
	// collectionConfirmations holds one record per (registration key, proposal
	// fingerprint, platform instance): that instance's decision on that exact
	// proposal. The fingerprint is in the key, so a decision can never be read
	// as applying to a proposal it was not made on.
	collectionConfirmations = "registration-confirmations"
	// collectionRegistrations holds only accepted registrations. Creating a
	// record here is the site's single commit point: a registration is accepted
	// when, and only when, its record exists.
	collectionRegistrations = "registrations"
)

// recordVersion is the encoding version stamped on every record this package
// writes. It is checked on read: an instance that does not understand a record
// refuses it rather than guessing at its meaning. It is part of the fingerprint,
// so proposals written by two encodings can never share one.
const recordVersion = 1

// Key is the immutable, site-local identity of one registration request. It is
// unique across the whole site fabric and is never scoped by machine: the
// machine a request arrived on is where it originated, not part of what it
// identifies.
type Key struct {
	// UnitType is the unit type identifier.
	UnitType uint8
	// UnitID is the unit identifier within UnitType.
	UnitID uint16
}

// String renders the key as the collection key locating its records.
func (k Key) String() string {
	return strconv.FormatUint(uint64(k.UnitType), 10) + "/" + strconv.FormatUint(uint64(k.UnitID), 10)
}

// Location is the trusted platform descriptor identity where a registration
// request originates. It is always derived from the receiving platform
// instance's own embedded descriptor, never from anything a client sends.
type Location struct {
	// Machine is the immutable descriptor machine name.
	Machine string
	// IP is the immutable descriptor IP address.
	IP string
}

// requestRecord is one stored proposal: the client's request fields, the origin
// the platform derived for it, and the fingerprint of that combination.
//
// It is written once and never updated. Registration is create-only, so the
// proposal a key was created with is the proposal it keeps for the site's life.
type requestRecord struct {
	// Version is the record encoding version.
	Version int `json:"version"`
	// UnitType is the requested unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the requested unit identifier within UnitType.
	UnitID uint16 `json:"unit_id"`
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the optional advertised role.
	Role *string `json:"role,omitempty"`
	// OriginMachine is the descriptor machine the request was submitted to.
	OriginMachine string `json:"origin_machine"`
	// OriginIP is the descriptor IP of that machine.
	OriginIP string `json:"origin_ip"`
	// Fingerprint is the proposal's deterministic content fingerprint.
	Fingerprint string `json:"fingerprint"`
}

// key returns the registration key the record belongs to.
func (r requestRecord) key() Key { return Key{UnitType: r.UnitType, UnitID: r.UnitID} }

// location returns the origin the request was submitted to.
func (r requestRecord) location() Location {
	return Location{Machine: r.OriginMachine, IP: r.OriginIP}
}

// sameProposal reports whether other asks for exactly what r asks for, from the
// same origin. These are every field a registration key is claimed on, so two
// records that agree here are the same claim and a second one is a retry.
func (r requestRecord) sameProposal(other requestRecord) bool {
	return r.UnitTypeNameAdvertised == other.UnitTypeNameAdvertised &&
		sameString(r.Role, other.Role) &&
		r.OriginMachine == other.OriginMachine &&
		r.OriginIP == other.OriginIP
}

// confirmationRecord is one platform instance's decision about one proposal.
//
// It is scoped to a fingerprint rather than to a registration key, because a
// decision is only meaningful about the data it was made on. It is written once
// per (key, fingerprint, machine) and never updated: an instance that has
// decided has decided.
type confirmationRecord struct {
	// Version is the record encoding version.
	Version int `json:"version"`
	// UnitType is the confirmed unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the confirmed unit identifier within UnitType.
	UnitID uint16 `json:"unit_id"`
	// Fingerprint is the fingerprint of the proposal this decision is about.
	Fingerprint string `json:"fingerprint"`
	// Machine is the descriptor machine of the deciding platform instance.
	Machine string `json:"machine"`
	// Status is accepted or rejected. There is no pending confirmation: an
	// instance that has not decided has no record.
	Status string `json:"status"`
	// Reason is the bounded machine-readable reason, present only on rejected.
	Reason *string `json:"reason,omitempty"`
}

// acceptedRecord is one committed registration. Its existence is what accepted
// means; nothing else in the site is authoritative for that.
type acceptedRecord struct {
	// Version is the record encoding version.
	Version int `json:"version"`
	// UnitType is the registered unit type identifier.
	UnitType uint8 `json:"unit_type"`
	// UnitID is the registered unit identifier within UnitType.
	UnitID uint16 `json:"unit_id"`
	// UnitTypeNameAdvertised is the unit's advertised type name.
	UnitTypeNameAdvertised string `json:"unit_type_name_advertised"`
	// Role is the optional advertised role.
	Role *string `json:"role,omitempty"`
	// OriginMachine is the descriptor machine the request originated on.
	OriginMachine string `json:"origin_machine"`
	// OriginIP is the descriptor IP of that machine.
	OriginIP string `json:"origin_ip"`
	// Fingerprint is the fingerprint of the proposal that was accepted.
	Fingerprint string `json:"fingerprint"`
}

// key returns the registration key the record belongs to.
func (r acceptedRecord) key() Key { return Key{UnitType: r.UnitType, UnitID: r.UnitID} }

// location returns the origin the registration was accepted from.
func (r acceptedRecord) location() Location {
	return Location{Machine: r.OriginMachine, IP: r.OriginIP}
}

// acceptedFrom builds the committed registration for an accepted proposal. The
// accepted record is a copy of the proposal, not a reference to it: what was
// committed stays readable as one record, whatever happens to anything else.
func acceptedFrom(request requestRecord) acceptedRecord {
	return acceptedRecord{
		Version:                recordVersion,
		UnitType:               request.UnitType,
		UnitID:                 request.UnitID,
		UnitTypeNameAdvertised: request.UnitTypeNameAdvertised,
		Role:                   copyString(request.Role),
		OriginMachine:          request.OriginMachine,
		OriginIP:               request.OriginIP,
		Fingerprint:            request.Fingerprint,
	}
}

// confirmationKey locates one instance's decision about one proposal.
//
// The fingerprint is in the key, which is what makes a stale decision harmless
// rather than dangerous: a confirmation of a proposal that is not the current
// one is simply at a key nothing looks up. Aggregation reads the confirmations
// of the current fingerprint by name and never enumerates, so a decision cannot
// approve data it was not made on.
func confirmationKey(key Key, fingerprint, machine string) string {
	return key.String() + "/" + fingerprint + "/" + machine
}

// fingerprintOf computes a proposal's deterministic content fingerprint from the
// canonical request fields plus the origin machine and IP.
//
// It exists to prove a confirmation refers to exactly this data. It is not a
// request id: it is derived, not generated, it identifies content rather than an
// occurrence, and it never leaves the platform. Two identical proposals have one
// fingerprint by design, because they are the same claim.
//
// Every field is length-prefixed, so no combination of values can encode the
// same bytes as a different combination.
func fingerprintOf(request requestRecord) string {
	hash := sha256.New()
	var canonical strings.Builder
	writeField(&canonical, strconv.Itoa(recordVersion))
	writeField(&canonical, strconv.FormatUint(uint64(request.UnitType), 10))
	writeField(&canonical, strconv.FormatUint(uint64(request.UnitID), 10))
	writeField(&canonical, request.UnitTypeNameAdvertised)
	// An absent role and a role that happens to be blank are different claims,
	// so presence is encoded rather than flattened away.
	if request.Role == nil {
		writeField(&canonical, "-")
	} else {
		writeField(&canonical, "+"+*request.Role)
	}
	writeField(&canonical, request.OriginMachine)
	writeField(&canonical, request.OriginIP)
	_, _ = hash.Write([]byte(canonical.String()))
	return hex.EncodeToString(hash.Sum(nil))
}

// writeField appends one length-prefixed field to a canonical encoding.
func writeField(canonical *strings.Builder, value string) {
	canonical.WriteString(strconv.Itoa(len(value)))
	canonical.WriteString(":")
	canonical.WriteString(value)
}

// encode renders a record as the bytes the fabric stores.
func encode(record any) ([]byte, error) {
	value, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("registration: encode %T: %w", record, err)
	}
	return value, nil
}

// decode reads a record the fabric returned. A record this package cannot
// decode is corruption, not a bad request: nothing outside this package writes
// these collections, so the error is reported rather than repaired.
func decode(value []byte, target any) error {
	if err := json.Unmarshal(value, target); err != nil {
		return fmt.Errorf("registration: decode %T: %w", target, err)
	}
	return nil
}

// copyString returns a copy of an optional string, so a stored or returned
// value never shares a pointer with a caller's.
func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	valueCopy := *value
	return &valueCopy
}

// sameString reports whether two optional strings are the same claim, treating
// absent and present-but-equal correctly.
func sameString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
