package scenarios

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file is the scenario side of the platform's event log: enough to find
// the JSONL file a platform process writes, read it, and match records in it.
// It is deliberately a black-box view. It re-declares the wire shape it needs
// instead of importing the platform's Go types, so a scenario checks the
// published contract and a change to it shows up here as a failing scenario
// rather than as a silently recompiled struct.

const (
	// maxEventLine bounds one scanned record. Events are small; the bound only
	// stops a corrupt file from exhausting memory.
	maxEventLine = 1024 * 1024
	// eventPollInterval is how often a wait re-reads the file.
	eventPollInterval = 50 * time.Millisecond
	// eventWaitTimeout bounds a wait for expected events.
	eventWaitTimeout = 15 * time.Second
)

// eventNode is the deployment identity of the platform process an event is
// about. It is not on the records: the platform states it once, by naming the
// file after it, so a scenario names the node it wants to read.
type eventNode struct {
	Project     string
	Environment string
	Site        string
	Machine     string
	Role        string
}

// fileName returns the file the platform records node's events in.
func (n eventNode) fileName() string {
	return fmt.Sprintf("events-%s-%s-%s-%s-%s.jsonl", n.Project, n.Environment, n.Site, n.Machine, n.Role)
}

// eventRecord is the observable shape of one recorded event.
type eventRecord struct {
	// ID is unique per occurrence.
	ID string `json:"id"`
	// Type is the stable dotted event kind.
	Type string `json:"type"`
	// Sequence is the process-local monotonic event number.
	Sequence uint64 `json:"sequence"`
	// OccurredAt is when the fact happened, in UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// Source is the subsystem that emitted the event.
	Source string `json:"source"`
	// Tags are optional markers, e.g. warning.
	Tags []string `json:"tags,omitempty"`
	// Data is the event-specific payload, decoded only when queried.
	Data json.RawMessage `json:"data"`
}

// payload decodes the event's data into a generic map, so a scenario can assert
// selected fields without declaring every payload type.
func (r eventRecord) payload(t *testing.T) map[string]any {
	t.Helper()
	if len(r.Data) == 0 || string(r.Data) == "null" {
		return map[string]any{}
	}
	var data map[string]any
	require.NoError(t, json.Unmarshal(r.Data, &data), "decode %s payload", r.Type)
	return data
}

// requireOnlyNodeInLog checks dir holds exactly one node's event log, and that
// it is node's. It is how a scenario checks identity now that the node is in
// the file name: the platform named the file from its own compiled-in
// descriptor, so a wrong identity is a wrong file name.
func requireOnlyNodeInLog(t *testing.T, dir string, node eventNode) {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, filepath.Base(entry))
	}
	require.Equal(t, []string{node.fileName()}, names)
}

// readEvents returns every event node recorded in dir, oldest first. A missing
// file reads as empty: a platform that has recorded nothing yet is not an error.
func readEvents(t *testing.T, dir string, node eventNode) []eventRecord {
	t.Helper()
	path := filepath.Join(dir, node.fileName())
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err, "open event log")
	defer func() { _ = file.Close() }()

	var records []eventRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxEventLine)
	for line := 1; scanner.Scan(); line++ {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var record eventRecord
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &record), "decode %s line %d", path, line)
		records = append(records, record)
	}
	require.NoError(t, scanner.Err(), "scan %s", path)
	return records
}

// waitForEventTypes polls dir until every type in want has been recorded, then
// returns the whole log. On timeout it reports what was actually recorded,
// which is the first thing worth knowing when a scenario fails.
func waitForEventTypes(t *testing.T, dir string, node eventNode, want ...string) []eventRecord {
	t.Helper()
	deadline := time.Now().Add(eventWaitTimeout)
	for {
		records := readEvents(t, dir, node)
		if hasEventTypes(records, want) {
			return records
		}
		if time.Now().After(deadline) {
			require.FailNow(t, "timed out waiting for events",
				"want %v\nrecorded %v", want, eventTypes(records))
			return nil
		}
		time.Sleep(eventPollInterval)
	}
}

// hasEventTypes reports whether every wanted type appears in records.
func hasEventTypes(records []eventRecord, want []string) bool {
	for _, eventType := range want {
		if len(eventsOfType(records, eventType)) == 0 {
			return false
		}
	}
	return true
}

// eventsOfType returns the records of one event type, in recorded order.
func eventsOfType(records []eventRecord, eventType string) []eventRecord {
	matched := make([]eventRecord, 0, len(records))
	for _, record := range records {
		if record.Type == eventType {
			matched = append(matched, record)
		}
	}
	return matched
}

// eventTypes returns the recorded event types, in recorded order.
func eventTypes(records []eventRecord) []string {
	types := make([]string, 0, len(records))
	for _, record := range records {
		types = append(types, record.Type)
	}
	return types
}

// requireEventOrder checks want appears in records in that relative order,
// ignoring unrelated events between them.
func requireEventOrder(t *testing.T, records []eventRecord, want ...string) {
	t.Helper()
	recorded := eventTypes(records)
	remaining := recorded
	for _, eventType := range want {
		index := slices.Index(remaining, eventType)
		require.GreaterOrEqual(t, index, 0,
			"expected %s in order %s, recorded %v", eventType, strings.Join(want, " -> "), recorded)
		remaining = remaining[index+1:]
	}
}

// requireSingleEvent returns the only record of eventType, failing with a
// readable message when it is missing or duplicated.
func requireSingleEvent(t *testing.T, records []eventRecord, eventType string) eventRecord {
	t.Helper()
	matched := eventsOfType(records, eventType)
	require.Len(t, matched, 1, "expected exactly one %s, recorded %v", eventType, eventTypes(records))
	return matched[0]
}

// eventsConfig renders a platform configuration file that pins the listen
// address and records events into eventsDir.
func eventsConfig(addr, eventsDir string) []byte {
	return fmt.Appendf(nil, "{\"address\": %q, \"events_dir\": %q}", addr, eventsDir)
}
