package registration

import (
	"context"
	"errors"
	"fmt"

	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
)

// store is registration's state as it lives on the platform fabric: three named
// collections, shared by every machine of the site.
//
// It is the only place that knows the collections exist. Everything above it
// works in records and keys, so what the site's state looks like on the wire is
// one file's business.
//
// There is no transaction across the three. Each write is atomic for its own
// key and nothing more, which is why every write here is create-if-absent and
// why creating the accepted record is the one commit point: a request and its
// confirmations can be written again harmlessly, and only one create of a
// registration can ever win.
type store struct {
	requests      fabric.Collection
	confirmations fabric.Collection
	registrations fabric.Collection
}

// openStore opens registration's collections on f. Opening is idempotent, so a
// restarted process reopens the site's existing state rather than a new copy of
// it.
func openStore(f fabric.Fabric) (*store, error) {
	if f == nil {
		return nil, errors.New("registration: fabric is required")
	}
	requests, err := f.Collection(collectionRequests)
	if err != nil {
		return nil, fmt.Errorf("registration: open %s: %w", collectionRequests, err)
	}
	confirmations, err := f.Collection(collectionConfirmations)
	if err != nil {
		return nil, fmt.Errorf("registration: open %s: %w", collectionConfirmations, err)
	}
	registrations, err := f.Collection(collectionRegistrations)
	if err != nil {
		return nil, fmt.Errorf("registration: open %s: %w", collectionRegistrations, err)
	}
	return &store{requests: requests, confirmations: confirmations, registrations: registrations}, nil
}

// createRequest claims a registration key for candidate.
//
// The claim is one atomic create-if-absent against the site, which is what makes
// two machines proposing one key at the same moment produce a winner and a
// conflict rather than two registrations. A request record is never removed, so
// the same claim also guards keys that have already been accepted.
//
// A losing create is not automatically a conflict: it is a conflict only if the
// record holding the key asks for something else. An identical proposal is an
// idempotent retry, and the stored record is returned either way, so a caller
// reports what the site actually holds rather than what the client sent.
func (s *store) createRequest(ctx context.Context, candidate requestRecord) (CreateResult, requestRecord, error) {
	value, err := encode(candidate)
	if err != nil {
		return "", requestRecord{}, err
	}
	created, err := s.requests.Create(ctx, candidate.key().String(), value)
	if err != nil {
		return "", requestRecord{}, fmt.Errorf("registration: create request %s: %w", candidate.key(), err)
	}
	if created {
		return CreateResultNew, candidate, nil
	}

	existing, found, err := s.request(ctx, candidate.key())
	if err != nil {
		return "", requestRecord{}, err
	}
	if !found {
		// The key was taken a moment ago and holds nothing now. Nothing removes
		// a request, so this cannot happen against a healthy site; saying so
		// beats retrying into a loop or inventing a state.
		return "", requestRecord{}, fmt.Errorf("registration: request %s was created by another writer but cannot be read", candidate.key())
	}
	if !existing.sameProposal(candidate) {
		return "", existing, ErrConflict
	}
	return CreateResultRetry, existing, nil
}

// request returns the proposal stored under key.
func (s *store) request(ctx context.Context, key Key) (requestRecord, bool, error) {
	value, found, err := s.requests.Get(ctx, key.String())
	if err != nil {
		return requestRecord{}, false, fmt.Errorf("registration: read request %s: %w", key, err)
	}
	if !found {
		return requestRecord{}, false, nil
	}
	var record requestRecord
	if err := decode(value, &record); err != nil {
		return requestRecord{}, false, err
	}
	if record.key() != key {
		return requestRecord{}, false, fmt.Errorf("registration: request stored under %s carries key %s", key, record.key())
	}
	return record, true, nil
}

// allRequests returns every registration request in the site, ordered by key.
//
// It enumerates requests rather than registrations, because a request that is
// pending or rejected is not in the registrations collection and is exactly what
// a caller asking "what is going on" needs to see.
//
// Enumeration is weakly consistent: it is not a snapshot of the site, and a
// request created while it runs may or may not appear. Every record it does
// return is whole.
func (s *store) allRequests(ctx context.Context) ([]requestRecord, error) {
	entries, err := s.requests.Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("registration: enumerate requests: %w", err)
	}
	records := make([]requestRecord, 0, len(entries))
	for _, entry := range entries {
		var record requestRecord
		if err := decode(entry.Value, &record); err != nil {
			return nil, err
		}
		if record.key().String() != entry.Key {
			return nil, fmt.Errorf("registration: request stored under %s carries key %s", entry.Key, record.key())
		}
		records = append(records, record)
	}
	return records, nil
}

// createConfirmation records one instance's decision about one proposal, unless
// that instance has already decided about it.
//
// It reports whether this call was the one that recorded the decision, which is
// how a repeated scan or a restarted process states its confirmation once
// instead of once per pass.
func (s *store) createConfirmation(ctx context.Context, record confirmationRecord) (bool, error) {
	value, err := encode(record)
	if err != nil {
		return false, err
	}
	key := confirmationKey(record.key(), record.Fingerprint, record.Machine)
	created, err := s.confirmations.Create(ctx, key, value)
	if err != nil {
		return false, fmt.Errorf("registration: create confirmation %s: %w", key, err)
	}
	return created, nil
}

// key returns the registration key the confirmation is about.
func (r confirmationRecord) key() Key { return Key{UnitType: r.UnitType, UnitID: r.UnitID} }

// decisions returns the decisions the expected platform instances have recorded
// about request, keyed by machine. An instance that has not decided is absent
// from the result rather than present as pending.
//
// It reads each expected instance's confirmation by name, at the current
// proposal's fingerprint. A confirmation of any other proposal therefore cannot
// appear here at all: it is not that a stale decision is found and discarded, it
// is that nothing ever asks for it.
func (s *store) decisions(ctx context.Context, request requestRecord, members []fabric.Member) (map[string]confirmationRecord, error) {
	decisions := make(map[string]confirmationRecord, len(members))
	for _, member := range members {
		key := confirmationKey(request.key(), request.Fingerprint, member.Machine)
		value, found, err := s.confirmations.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("registration: read confirmation %s: %w", key, err)
		}
		if !found {
			continue
		}
		var record confirmationRecord
		if err := decode(value, &record); err != nil {
			return nil, err
		}
		if record.Machine != member.Machine || record.Fingerprint != request.Fingerprint || record.key() != request.key() {
			return nil, fmt.Errorf("registration: confirmation stored under %s carries %s/%s/%s",
				key, record.key(), record.Fingerprint, record.Machine)
		}
		decisions[member.Machine] = record
	}
	return decisions, nil
}

// createAccepted commits a registration, reporting whether this call committed
// it. This is the site's single commit point, and it is create-only: a key that
// holds a registration holds it for the site's life.
func (s *store) createAccepted(ctx context.Context, record acceptedRecord) (bool, error) {
	value, err := encode(record)
	if err != nil {
		return false, err
	}
	created, err := s.registrations.Create(ctx, record.key().String(), value)
	if err != nil {
		return false, fmt.Errorf("registration: create registration %s: %w", record.key(), err)
	}
	return created, nil
}

// accepted returns the committed registration under key.
func (s *store) accepted(ctx context.Context, key Key) (acceptedRecord, bool, error) {
	value, found, err := s.registrations.Get(ctx, key.String())
	if err != nil {
		return acceptedRecord{}, false, fmt.Errorf("registration: read registration %s: %w", key, err)
	}
	if !found {
		return acceptedRecord{}, false, nil
	}
	var record acceptedRecord
	if err := decode(value, &record); err != nil {
		return acceptedRecord{}, false, err
	}
	if record.key() != key {
		return acceptedRecord{}, false, fmt.Errorf("registration: registration stored under %s carries key %s", key, record.key())
	}
	return record, true, nil
}
