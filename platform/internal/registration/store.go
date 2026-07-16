package registration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/miroslav-matejovsky/opdl/platform/internal/fabric"
)

// store owns registration's fabric collections. Immutable contenders and
// acceptance markers are the source of truth; request and accepted records are
// projections that reconciliation can overwrite after a membership change.
type store struct {
	contenderRecords fabric.Collection
	requests         fabric.Collection
	confirmations    fabric.Collection
	acceptances      fabric.Collection
	registrations    fabric.Collection
}

// openStore opens registration's collections on f. Opening is idempotent, so a
// restarted process reopens the site's existing state rather than a new copy of
// it.
func openStore(f fabric.Fabric) (*store, error) {
	if f == nil {
		return nil, errors.New("registration: fabric is required")
	}
	contenders, err := f.Collection(collectionContenders)
	if err != nil {
		return nil, fmt.Errorf("registration: open %s: %w", collectionContenders, err)
	}
	requests, err := f.Collection(collectionRequests)
	if err != nil {
		return nil, fmt.Errorf("registration: open %s: %w", collectionRequests, err)
	}
	confirmations, err := f.Collection(collectionConfirmations)
	if err != nil {
		return nil, fmt.Errorf("registration: open %s: %w", collectionConfirmations, err)
	}
	acceptances, err := f.Collection(collectionAcceptances)
	if err != nil {
		return nil, fmt.Errorf("registration: open %s: %w", collectionAcceptances, err)
	}
	registrations, err := f.Collection(collectionRegistrations)
	if err != nil {
		return nil, fmt.Errorf("registration: open %s: %w", collectionRegistrations, err)
	}
	return &store{
		contenderRecords: contenders,
		requests:         requests,
		confirmations:    confirmations,
		acceptances:      acceptances,
		registrations:    registrations,
	}, nil
}

// createRequest records candidate as an immutable contender. A visible current
// projection still rejects a different proposal immediately, but a contender
// that races that read is retained and returns new rather than being silently
// erased by a false Create win during a member join.
func (s *store) createRequest(ctx context.Context, candidate requestRecord) (CreateResult, requestRecord, error) {
	existing, found, err := s.request(ctx, candidate.key())
	if err != nil {
		return "", requestRecord{}, err
	}
	if found && !existing.sameProposal(candidate) {
		return "", existing, ErrConflict
	}

	value, err := encode(candidate)
	if err != nil {
		return "", requestRecord{}, err
	}
	key := contenderKey(candidate.key(), candidate.Fingerprint)
	created, err := s.contenderRecords.Create(ctx, key, value)
	if err != nil {
		return "", requestRecord{}, fmt.Errorf("registration: create contender %s: %w", key, err)
	}
	if !created {
		existing, found, err := s.contender(ctx, candidate.key(), candidate.Fingerprint)
		if err != nil {
			return "", requestRecord{}, err
		}
		if !found {
			return "", requestRecord{}, fmt.Errorf("registration: contender %s was created by another writer but cannot be read", key)
		}
		if !existing.sameProposal(candidate) {
			return "", existing, fmt.Errorf("registration: contender %s has a different proposal", key)
		}
		return CreateResultRetry, existing, nil
	}

	// This view makes an already visible conflict cheap to reject. It is not
	// authoritative: a false Create win can overwrite it, and reconciliation
	// restores the selected contender from the immutable collection.
	if _, err := s.requests.Create(ctx, candidate.key().String(), value); err != nil {
		return "", requestRecord{}, fmt.Errorf("registration: create request projection %s: %w", candidate.key(), err)
	}
	return CreateResultNew, candidate, nil
}

// request returns the current request projection stored under key.
func (s *store) request(ctx context.Context, key Key) (requestRecord, bool, error) {
	value, found, err := s.requests.Get(ctx, key.String())
	if err != nil {
		return requestRecord{}, false, fmt.Errorf("registration: read request projection %s: %w", key, err)
	}
	if !found {
		return requestRecord{}, false, nil
	}
	var record requestRecord
	if err := decode(value, &record); err != nil {
		return requestRecord{}, false, err
	}
	if record.key() != key {
		return requestRecord{}, false, fmt.Errorf("registration: request projection stored under %s carries key %s", key, record.key())
	}
	return record, true, nil
}

// contender returns the immutable contender identified by key and fingerprint.
func (s *store) contender(ctx context.Context, key Key, fingerprint string) (requestRecord, bool, error) {
	storageKey := contenderKey(key, fingerprint)
	value, found, err := s.contenderRecords.Get(ctx, storageKey)
	if err != nil {
		return requestRecord{}, false, fmt.Errorf("registration: read contender %s: %w", storageKey, err)
	}
	if !found {
		return requestRecord{}, false, nil
	}
	var record requestRecord
	if err := decode(value, &record); err != nil {
		return requestRecord{}, false, err
	}
	if record.key() != key || record.Fingerprint != fingerprint {
		return requestRecord{}, false, fmt.Errorf("registration: contender stored under %s carries %s/%s", storageKey, record.key(), record.Fingerprint)
	}
	return record, true, nil
}

// allRequests returns every immutable contender in deterministic key, observed
// time, and fingerprint order. Enumeration is weakly consistent, so a contender
// written while it runs can appear on a later reconciliation pass instead.
func (s *store) allRequests(ctx context.Context) ([]requestRecord, error) {
	entries, err := s.contenderRecords.Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("registration: enumerate contenders: %w", err)
	}
	records := make([]requestRecord, 0, len(entries))
	for _, entry := range entries {
		var record requestRecord
		if err := decode(entry.Value, &record); err != nil {
			return nil, err
		}
		if entry.Key != contenderKey(record.key(), record.Fingerprint) {
			return nil, fmt.Errorf("registration: contender stored under %s carries %s/%s", entry.Key, record.key(), record.Fingerprint)
		}
		records = append(records, record)
	}
	sortContenders(records)
	return records, nil
}

// contenders returns the contenders observed for one registration key.
func (s *store) contenders(ctx context.Context, key Key) ([]requestRecord, error) {
	entries, err := s.contenderRecords.Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("registration: enumerate contenders: %w", err)
	}
	prefix := key.String() + "/"
	contenders := make([]requestRecord, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Key, prefix) {
			continue
		}
		var record requestRecord
		if err := decode(entry.Value, &record); err != nil {
			return nil, err
		}
		if entry.Key != contenderKey(record.key(), record.Fingerprint) {
			return nil, fmt.Errorf("registration: contender stored under %s carries %s/%s", entry.Key, record.key(), record.Fingerprint)
		}
		contenders = append(contenders, record)
	}
	sortContenders(contenders)
	return contenders, nil
}

// sortContenders orders contenders without relying on weak enumeration order.
func sortContenders(contenders []requestRecord) {
	sort.Slice(contenders, func(i, j int) bool {
		left, right := contenders[i], contenders[j]
		if left.key() != right.key() {
			return left.key().String() < right.key().String()
		}
		if !left.ObservedAt.Equal(right.ObservedAt) {
			return left.ObservedAt.Before(right.ObservedAt)
		}
		return left.Fingerprint < right.Fingerprint
	})
}

// createConfirmation records one instance's decision about one proposal, unless
// that instance has already decided about it.
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
// about request, keyed by machine.
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
			return nil, fmt.Errorf("registration: confirmation stored under %s carries %s/%s/%s", key, record.key(), record.Fingerprint, record.Machine)
		}
		decisions[member.Machine] = record
	}
	return decisions, nil
}

// createAcceptance records immutable evidence that request was accepted.
func (s *store) createAcceptance(ctx context.Context, record acceptanceRecord) (bool, error) {
	value, err := encode(record)
	if err != nil {
		return false, err
	}
	key := acceptanceKey(record.key(), record.Fingerprint)
	created, err := s.acceptances.Create(ctx, key, value)
	if err != nil {
		return false, fmt.Errorf("registration: create acceptance %s: %w", key, err)
	}
	return created, nil
}

// acceptance returns immutable acceptance evidence for request.
func (s *store) acceptance(ctx context.Context, request requestRecord) (acceptanceRecord, bool, error) {
	key := acceptanceKey(request.key(), request.Fingerprint)
	value, found, err := s.acceptances.Get(ctx, key)
	if err != nil {
		return acceptanceRecord{}, false, fmt.Errorf("registration: read acceptance %s: %w", key, err)
	}
	if !found {
		return acceptanceRecord{}, false, nil
	}
	var record acceptanceRecord
	if err := decode(value, &record); err != nil {
		return acceptanceRecord{}, false, err
	}
	if record.key() != request.key() || record.Fingerprint != request.Fingerprint {
		return acceptanceRecord{}, false, fmt.Errorf("registration: acceptance stored under %s carries %s/%s", key, record.key(), record.Fingerprint)
	}
	return record, true, nil
}

// winner selects the incumbent accepted before every competing contender was
// observed. If none qualifies, it uses observed time and fingerprint order. The
// returned bool reports whether the selected contender has acceptance evidence.
func (s *store) winner(ctx context.Context, contenders []requestRecord) (requestRecord, bool, error) {
	if len(contenders) == 0 {
		return requestRecord{}, false, errors.New("registration: select winner from no contenders")
	}
	ordered := append([]requestRecord(nil), contenders...)
	sortContenders(ordered)

	type accepted struct {
		request requestRecord
		marker  acceptanceRecord
	}
	incumbents := make([]accepted, 0, len(ordered))
	markers := make(map[string]acceptanceRecord, len(ordered))
	for _, contender := range ordered {
		marker, found, err := s.acceptance(ctx, contender)
		if err != nil {
			return requestRecord{}, false, err
		}
		if !found {
			continue
		}
		markers[contender.Fingerprint] = marker
		incumbent := true
		for _, other := range ordered {
			if contender.Fingerprint == other.Fingerprint {
				continue
			}
			if !marker.AcceptedAt.Before(other.ObservedAt) {
				incumbent = false
				break
			}
		}
		if incumbent {
			incumbents = append(incumbents, accepted{request: contender, marker: marker})
		}
	}
	if len(incumbents) != 0 {
		sort.Slice(incumbents, func(i, j int) bool {
			if !incumbents[i].marker.AcceptedAt.Equal(incumbents[j].marker.AcceptedAt) {
				return incumbents[i].marker.AcceptedAt.Before(incumbents[j].marker.AcceptedAt)
			}
			return incumbents[i].request.Fingerprint < incumbents[j].request.Fingerprint
		})
		return incumbents[0].request, true, nil
	}
	winner := ordered[0]
	_, acceptedWinner := markers[winner.Fingerprint]
	return winner, acceptedWinner, nil
}

// setRequest repairs the current request projection to request.
func (s *store) setRequest(ctx context.Context, request requestRecord) error {
	value, err := encode(request)
	if err != nil {
		return err
	}
	if _, _, err := s.requests.Swap(ctx, request.key().String(), value); err != nil {
		return fmt.Errorf("registration: repair request projection %s: %w", request.key(), err)
	}
	return nil
}

// setAccepted repairs the current accepted projection to record.
func (s *store) setAccepted(ctx context.Context, record acceptedRecord) error {
	value, err := encode(record)
	if err != nil {
		return err
	}
	if _, _, err := s.registrations.Swap(ctx, record.key().String(), value); err != nil {
		return fmt.Errorf("registration: repair accepted projection %s: %w", record.key(), err)
	}
	return nil
}

// accepted returns the current accepted projection under key.
func (s *store) accepted(ctx context.Context, key Key) (acceptedRecord, bool, error) {
	value, found, err := s.registrations.Get(ctx, key.String())
	if err != nil {
		return acceptedRecord{}, false, fmt.Errorf("registration: read accepted projection %s: %w", key, err)
	}
	if !found {
		return acceptedRecord{}, false, nil
	}
	var record acceptedRecord
	if err := decode(value, &record); err != nil {
		return acceptedRecord{}, false, err
	}
	if record.key() != key {
		return acceptedRecord{}, false, fmt.Errorf("registration: accepted projection stored under %s carries key %s", key, record.key())
	}
	return record, true, nil
}
